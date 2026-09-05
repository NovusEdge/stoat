package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/project"
)

// projectRoot sets up a data root, writes stoat.toml into a separate project
// directory and chdirs into it. Every project CLI test starts here.
func projectRoot(t *testing.T, body string) string {
	t.Helper()
	cliRoot(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

const twoVMs = `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image = "alpine-virt"

[vms.ci]
image = "alpine-virt"
`

// A bare key reaches the global name, so stoat get dev answers about
// myrepo-dev.
func TestBareKeyResolvesToTheGlobalName(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "get", "dev")
	data, _ := result(t, objs)["data"].(map[string]any)
	vm, _ := data["vm"].(map[string]any)
	if vm["name"] != "myrepo-dev" {
		t.Errorf("name = %v, want myrepo-dev", vm["name"])
	}
}

// An argument that is neither a key nor an existing VM names both places it
// was looked for.
func TestUnknownBareArgumentNamesBothScopes(t *testing.T) {
	projectRoot(t, twoVMs)
	code, objs := runJSON(t, "get", "db")
	if code != ExitFail {
		t.Errorf("exit = %d, want %d", code, ExitFail)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, `no VM "db" in stoat.toml or ~/.stoat/vms`) {
		t.Errorf("message = %q", msg)
	}
}

// A global name still works inside a project: the project shadows nothing it
// did not declare.
func TestGlobalNameStillWorksAtProjectScope(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	code, _ := runJSON(t, "get", "other")
	if code != ExitOK {
		t.Errorf("exit = %d, want %d for a global VM at project scope", code, ExitOK)
	}
}

// Outside a project, a missing VM argument is still a usage error with the
// same exit code kong produced before the positional became optional.
func TestMissingVMArgumentOutsideAProjectIsUsage(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	code, objs := runJSON(t, "up")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	errObj, _ := result(t, objs)["error"].(map[string]any)
	if errObj["code"] != "usage" {
		t.Errorf("code = %v, want usage", errObj["code"])
	}
}

// pull's positional is an image id, not a VM name, so the project must not
// rewrite it.
func TestPullArgumentIsNotResolved(t *testing.T) {
	projectRoot(t, twoVMs)
	a := &Args{Cmd: "pull", VM: "dev"}
	if err := resolveScope(a); err != nil {
		t.Fatal(err)
	}
	if a.VM != "dev" {
		t.Errorf("pull id = %q, want it untouched", a.VM)
	}
}
