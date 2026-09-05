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
