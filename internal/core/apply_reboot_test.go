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
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl", Installed: true,
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

func TestApplyRunsHealthOnlyAfterRebootAndReachability(t *testing.T) {
	dir := root(t)
	writeSchema3RebootHealthRecipe(t, dir, "xfce")
	sequence := filepath.Join(dir, "ssh-sequence")
	installSequenceSSH(t, sequence)

	port, stop := fakeSSHD(t, 0)
	defer stop()
	v := &config.VM{
		Name: "work", Mode: "disk", OS: "alpine", Backend: "apkovl", Installed: true,
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"xfce"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	if err := Apply(context.Background(), v.Name, ApplyOpts{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	events, err := os.ReadFile(sequence)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(events))
	index := func(want string) int {
		for i, line := range lines {
			if line == want {
				return i
			}
		}
		return -1
	}
	recipeAt, rebootAt, healthAt := index("recipe"), index("reboot"), index("health")
	if recipeAt < 0 || rebootAt < 0 || healthAt < 0 {
		t.Fatalf("ssh sequence = %v, want recipe, reboot, and health", lines)
	}
	if !(recipeAt < rebootAt && rebootAt < healthAt) {
		t.Fatalf("ssh sequence = %v, want recipe < reboot < health", lines)
	}
}

func writeSchema3RebootHealthRecipe(t *testing.T, rootDir, name string) {
	t.Helper()
	d := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"" + name + "\"\nversion = \"1.0\"\nscript = \"install.sh\"\nrun = \"always\"\nreboot = true\n\n[health]\ncheck = \"health-check\"\n"
	if err := os.WriteFile(filepath.Join(d, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "install.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func installSequenceSSH(t *testing.T, sequence string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"last=\"\"\nfor a in \"$@\"; do last=\"$a\"; done\n" +
		"if [ \"$last\" = reboot ]; then printf 'reboot\\n' >> " + shellQuoteCoreTest(sequence) + "; exit 0; fi\n" +
		"input=$(cat)\n" +
		"case \"$*\" in *'cat /tmp/.stoat-out/xfce'*) printf 'outputs\\n' >> " + shellQuoteCoreTest(sequence) + "; exit 0;; esac\n" +
		"case \"$input\" in *'health-check'*) printf 'health\\n' >> " + shellQuoteCoreTest(sequence) + "; exit 0;; *'STOAT_RECIPE=xfce'*) printf 'recipe\\n' >> " + shellQuoteCoreTest(sequence) + "; exit 0;; *'stoat_pkg_setup'*) printf 'setup\\n' >> " + shellQuoteCoreTest(sequence) + "; exit 0;; esac\n" +
		"printf 'other\\n' >> " + shellQuoteCoreTest(sequence) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestApplyDoesNotRebootALiveVM pins the mode gate: a live VM's root is a
// tmpfs the reboot wipes, and a live VM re-applies every boot, so a reboot
// here would loop. A reboot=true recipe on a live VM reboots nothing.
func TestApplyDoesNotRebootALiveVM(t *testing.T) {
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
	if strings.Contains(string(log), "rebooting") {
		t.Errorf("a live VM rebooted: %q", log)
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
