package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestStatusLineFormat(t *testing.T) {
	projectRoot(t, `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image = "alpine-virt"
cpus  = 4

[vms.ci]
image = "alpine-virt"
`)
	haveImage(t, os.Getenv("STOAT_HOME"), "alpine-virt-3.24.1-x86_64.iso")
	// dev exists at 2 cpus, so the declaration drifts by two.
	if err := (&config.VM{
		Name: "myrepo-dev", Mode: "live", ISO: "isos/alpine-virt-3.24.1-x86_64.iso",
		RAM: 4096, CPUs: 2, SSHPort: 2200,
	}).Save(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Main([]string{"status"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header and two VMs:\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "KEY") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "myrepo-dev") || !strings.Contains(lines[1], "cpus 2 → 4 (restart)") {
		t.Errorf("dev line = %q", lines[1])
	}
	if !strings.Contains(lines[2], "missing") {
		t.Errorf("ci line = %q, want state missing", lines[2])
	}
}

func TestStatusJSON(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "live", ISO: "alpine-virt.iso", RAM: 4096, CPUs: 4, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "status")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	data, _ := result(t, objs)["data"].(map[string]any)
	if data["project"] != "myrepo" {
		t.Errorf("project = %v, want myrepo", data["project"])
	}
	vms, _ := data["vms"].([]any)
	if len(vms) != 2 {
		t.Fatalf("vms = %v, want two", vms)
	}
	first, _ := vms[0].(map[string]any)
	if first["key"] != "dev" || first["name"] != "myrepo-dev" {
		t.Errorf("first = %v", first)
	}
	if _, ok := first["drift"].([]any); !ok {
		t.Errorf("drift = %#v, want an array, never null", first["drift"])
	}
}

// Outside a project, status has nothing to report and says so rather than
// printing an empty table.
func TestStatusOutsideAProject(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	code, objs := runJSON(t, "status")
	if code != ExitFail {
		t.Errorf("exit = %d, want %d", code, ExitFail)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "no stoat.toml") {
		t.Errorf("message = %q", msg)
	}
}
