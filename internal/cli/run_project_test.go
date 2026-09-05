package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// haveImage drops a placeholder ISO into <home>/isos so core.Create resolves
// the alpine-virt catalog entry without reaching the network. Mirrors
// internal/core's own haveImage, unexported in a different package.
func haveImage(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, "isos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The fan-out runs in declaration order, stops at the first failure and
// reports every VM after it as skipped, so a half-built project is readable.
// down refuses every fixture VM here, since none are running; "one" is first
// in declaration order, so it is the first failure.
func TestFanOutStopsAtTheFirstError(t *testing.T) {
	projectRoot(t, `
schema = 1

[project]
name = "myrepo"

[vms.one]
image = "alpine-virt"

[vms.two]
image = "alpine-virt"

[vms.three]
image = "alpine-virt"
`)
	for _, n := range []string{"myrepo-one", "myrepo-two", "myrepo-three"} {
		if err := (&config.VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
			t.Fatal(err)
		}
	}
	code, objs := runJSON(t, "down")
	if code != ExitFail {
		t.Errorf("exit = %d, want %d", code, ExitFail)
	}
	data, _ := result(t, objs)["data"].(map[string]any)
	vms, _ := data["vms"].([]any)
	if len(vms) != 3 {
		t.Fatalf("vms = %v, want three entries", vms)
	}
	want := []struct{ key, status string }{
		{"one", "error"}, {"two", "skipped"}, {"three", "skipped"},
	}
	for i, w := range want {
		e, _ := vms[i].(map[string]any)
		if e["key"] != w.key || e["status"] != w.status {
			t.Errorf("vms[%d] = %v, want %s/%s", i, e, w.key, w.status)
		}
	}
}

// stoat up with no argument creates every declared VM that is missing.
func TestUpWithNoArgumentReconcilesEveryDeclaration(t *testing.T) {
	projectRoot(t, twoVMs)
	haveImage(t, os.Getenv("STOAT_HOME"), "alpine-virt-3.24.1-x86_64.iso")
	// --no-apply keeps the run host-side: nothing boots in a unit test.
	runJSON(t, "up", "--no-apply")
	for _, n := range []string{"myrepo-dev", "myrepo-ci"} {
		if _, err := config.Load(n); err != nil {
			t.Errorf("%s was not created: %v", n, err)
		}
	}
}

// --json never prompts, so rm with no -y is an error, not a question, at
// project scope too.
func TestRMAtProjectScopeRefusesWithoutYesUnderJSON(t *testing.T) {
	projectRoot(t, twoVMs)
	for _, n := range []string{"myrepo-dev", "myrepo-ci"} {
		if err := (&config.VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
			t.Fatal(err)
		}
	}
	code, _ := runJSON(t, "rm")
	if code != ExitFail {
		t.Errorf("exit = %d, want %d", code, ExitFail)
	}
	if _, err := config.Load("myrepo-dev"); err != nil {
		t.Error("myrepo-dev was deleted without -y under --json")
	}
}

// apply with no argument runs every declaration in order. The fixture VMs are
// stopped, so every entry fails; the point is that all three are attempted in
// order and the report names them.
func TestApplyWithNoArgumentCoversEveryDeclaration(t *testing.T) {
	projectRoot(t, twoVMs)
	for _, n := range []string{"myrepo-dev", "myrepo-ci"} {
		if err := (&config.VM{Name: n, Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
			t.Fatal(err)
		}
	}
	_, objs := runJSON(t, "apply", "--dry-run")
	data, _ := result(t, objs)["data"].(map[string]any)
	vms, _ := data["vms"].([]any)
	if len(vms) != 2 {
		t.Fatalf("vms = %v, want two entries", vms)
	}
	first, _ := vms[0].(map[string]any)
	if first["key"] != "dev" {
		t.Errorf("first entry = %v, want dev, the first declaration", first)
	}
}

func TestLSShowsProjectAndKey(t *testing.T) {
	dir := projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Project: dir}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "ls")
	data, _ := result(t, objs)["data"].(map[string]any)
	vms, _ := data["vms"].([]any)
	byName := map[string]map[string]any{}
	for _, v := range vms {
		m, _ := v.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["myrepo-dev"]["key"] != "dev" {
		t.Errorf("myrepo-dev key = %v, want dev", byName["myrepo-dev"]["key"])
	}
	if byName["myrepo-dev"]["project"] != dir {
		t.Errorf("myrepo-dev project = %v, want %q", byName["myrepo-dev"]["project"], dir)
	}
	if byName["other"]["project"] != "" || byName["other"]["key"] != "" {
		t.Errorf("global VM carries project fields: %v", byName["other"])
	}
}

func TestLSProjectFilter(t *testing.T) {
	dir := projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Project: dir}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "ls", "--project")
	data, _ := result(t, objs)["data"].(map[string]any)
	vms, _ := data["vms"].([]any)
	if len(vms) != 1 {
		t.Fatalf("vms = %v, want only this project's", vms)
	}
}

// A VM whose project directory is gone still lists; ls marks the path.
func TestLSMarksAMissingProjectDirectory(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	if err := (&config.VM{Name: "orphan", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Project: "/gone/myrepo"}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "ls")
	data, _ := result(t, objs)["data"].(map[string]any)
	vms, _ := data["vms"].([]any)
	m, _ := vms[0].(map[string]any)
	if m["project_missing"] != true {
		t.Errorf("project_missing = %v, want true", m["project_missing"])
	}
}
