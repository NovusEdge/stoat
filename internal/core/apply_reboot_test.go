package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// installFakeSSHClient puts a stand-in "ssh" on PATH ahead of the real one,
// mirroring internal/sshx/sshx_test.go's installFakeSSH. It exits 0 for
// every invocation: a recipe run (its last two args are "sh" "-s") reads and
// discards stdin the way a real remote shell would consume it; a `reboot`
// invocation (its last arg is "reboot") exits immediately, standing in for
// ssh losing its connection when the guest actually reboots.
func installFakeSSHClient(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"last=\"\"\n" +
		"for a in \"$@\"; do last=\"$a\"; done\n" +
		"[ \"$last\" = reboot ] && exit 0\n" +
		"cat >/dev/null\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// writeV2RecipeWithReboot is writeV2Recipe (apply_test.go) plus the
// reboot=true line neither that helper nor recipes' own writeV2Recipe
// support.
func writeV2RecipeWithReboot(t *testing.T, rootDir, name string) {
	t.Helper()
	recipeDir := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"" + name + "\"\n" +
		"version = \"1.0\"\n" +
		"script = \"install.sh\"\n" +
		"run = \"always\"\n" +
		"reboot = true\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestApplyRebootsAfterARecipeThatDeclaresIt exercises the finalize step
// end to end: a successful run of a reboot=true recipe reboots the guest
// and waits for it to come back, logging both.
func TestApplyRebootsAfterARecipeThatDeclaresIt(t *testing.T) {
	dir := root(t)
	writeV2RecipeWithReboot(t, dir, "xfce")
	installFakeSSHClient(t)

	port, stop := fakeSSHD(t, 0)
	defer stop()

	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"xfce"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stopRunning := fakeRunning(t, v)
	defer stopRunning()

	if err := Apply(context.Background(), "work", ApplyOpts{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "rebooting work to finish xfce") {
		t.Errorf("provision log = %q, want a rebooting line naming xfce", log)
	}
}

// TestApplySkipsRebootWhenNoRecipeDeclaresIt is the negative case: a run
// with no reboot=true recipe among runTargets must not touch ssh a second
// time or log anything about rebooting.
func TestApplySkipsRebootWhenNoRecipeDeclaresIt(t *testing.T) {
	dir := root(t)
	writeV2Recipe(t, dir, "tool", "always", "1.0", "#!/bin/sh\necho hi\n")
	installFakeSSHClient(t)

	port, stop := fakeSSHD(t, 0)
	defer stop()

	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"tool"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stopRunning := fakeRunning(t, v)
	defer stopRunning()

	if err := Apply(context.Background(), "work", ApplyOpts{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "rebooting") {
		t.Errorf("provision log = %q, want no reboot line: no recipe declared reboot=true", log)
	}
}
