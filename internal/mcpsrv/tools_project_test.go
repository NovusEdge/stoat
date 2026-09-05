package mcpsrv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/project"
)

// inProject writes a stoat.toml into a fresh directory and chdirs there, so
// the server's cwd is a project when callTool builds it.
func inProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// haveImage drops a file into isos/ so alpine-virt counts as downloaded,
// without ever exec'ing qemu: core.Create for a live-mode image writes
// vm.toml only, and this package must never boot a real VM to fill it.
func haveImage(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "isos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpine-virt-3.24.1-x86_64.iso"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const twoDecls = `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image = "alpine-virt"

[vms.ci]
image = "alpine-virt"
`

// list_vms carries the declaration key, so an agent can say "the dev VM" and
// mean what stoat.toml means. Only "dev" has a VM on disk; "ci" is declared
// but never created, so it must not appear.
func TestListVMsCarriesProjectAndKey(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	inProject(t, twoDecls)
	writeVM(t, "myrepo-dev", "manage")

	res := callTool(t, "list_vms", map[string]any{})
	raw, _ := json.Marshal(res.StructuredContent)
	var out wire.VMList
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.VMs) != 1 || out.VMs[0].Key != "dev" {
		t.Errorf("vms = %+v, want one keyed dev", out.VMs)
	}
}

// project_status is read-only and reports a field where a declaration and
// vm.toml disagree, the same comparison core.Diff makes for stoat status.
func TestProjectStatusReportsDrift(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	haveImage(t, home)
	dir := inProject(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"alpine-virt\"\ncpus = 2\n")

	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}
	redeclared := "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"alpine-virt\"\ncpus = 8\n"
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(redeclared), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tool := range listTools(t) {
		if tool.Name == "project_status" && !tool.Annotations.ReadOnlyHint {
			t.Error("project_status is not annotated read-only")
		}
	}
	res := callTool(t, "project_status", map[string]any{})
	raw, _ := json.Marshal(res.StructuredContent)
	var out wire.ProjectStatus
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.VMs) != 1 {
		t.Fatalf("vms = %+v, want one", out.VMs)
	}
	row := out.VMs[0]
	var drift *wire.Drift
	for i := range row.Drift {
		if row.Drift[i].Field == "cpus" {
			drift = &row.Drift[i]
		}
	}
	if drift == nil || drift.From != "2" || drift.To != "8" || !drift.NeedsRestart {
		t.Errorf("cpus drift = %+v, want 2 -> 8, needs restart", row.Drift)
	}
}

// project_up reconciles every declaration in order and stops at the first
// failure, reporting the rest as skipped. Neither declaration's image is
// downloaded, so Reconcile refuses before core.Start would ever run: this
// package must not boot a real VM to observe the fan-out.
func TestProjectUpStopsAtFirstFailureInDeclarationOrder(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	inProject(t, twoDecls)

	res := callTool(t, "project_up", map[string]any{})
	if res.IsError {
		t.Fatalf("project_up errored at the tool level: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out wire.ProjectRun
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.VMs) != 2 || out.VMs[0].Key != "dev" || out.VMs[1].Key != "ci" {
		t.Fatalf("vms = %+v, want dev then ci", out.VMs)
	}
	if out.VMs[0].Status != "error" || out.VMs[0].Error == "" {
		t.Errorf("dev = %+v, want a reconcile error", out.VMs[0])
	}
	if out.VMs[1].Status != "skipped" {
		t.Errorf("ci = %+v, want skipped after dev failed", out.VMs[1])
	}
}

// project_apply gates each declared VM at its own agent_access, the same
// level apply_recipes needs. A VM below manage fails its own entry and the
// rest of the run is skipped, rather than the whole call being refused.
func TestProjectApplyGatesAccessPerVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	inProject(t, twoDecls)
	writeVM(t, "myrepo-dev", "observe")
	writeVM(t, "myrepo-ci", "manage")

	res := callTool(t, "project_apply", map[string]any{})
	raw, _ := json.Marshal(res.StructuredContent)
	var out wire.ProjectRun
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.VMs) != 2 {
		t.Fatalf("vms = %+v, want two", out.VMs)
	}
	if out.VMs[0].Status != "error" || !strings.Contains(out.VMs[0].Error, "agent_access =") {
		t.Errorf("dev = %+v, want an access refusal", out.VMs[0])
	}
	if out.VMs[1].Status != "skipped" {
		t.Errorf("ci = %+v, want skipped after dev's access refusal", out.VMs[1])
	}
}

// Every project tool is refused outside a project, and the message names the
// file the agent has to create.
func TestProjectToolsRefusedOutsideAProject(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	for _, name := range []string{"project_status", "project_up", "project_down", "project_apply", "project_wait"} {
		res := callTool(t, name, map[string]any{})
		if !res.IsError {
			t.Errorf("%s succeeded outside a project", name)
			continue
		}
		raw, _ := json.Marshal(res.Content)
		if !strings.Contains(string(raw), "stoat.toml") {
			t.Errorf("%s refusal does not name stoat.toml: %s", name, raw)
		}
	}
}
