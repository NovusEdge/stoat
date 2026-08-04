package recipes

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// allShellRecipes are the per-OS ssh-pushed recipes; allNames also includes
// the cloud-config fragment.
var allShellRecipes = []string{
	"xfce.alpine.sh", "xfce.ubuntu.sh", "xfce.arch.sh", "xfce.debian.sh",
	"docker.alpine.sh", "devtools.alpine.sh", "tailscale.alpine.sh",
}

func TestInstallCopiesBundledRecipesAndPreservesEdits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	if err := Install(); err != nil {
		t.Fatal(err)
	}

	for _, name := range append(append([]string{}, allShellRecipes...), "xfce.cloud.yaml", "xfce.fedora.cloud.yaml", "xfce.alpine.cloud.yaml", "devtools.cloud.yaml") {
		if _, err := os.Stat(Path(name)); err != nil {
			t.Errorf("Install did not install %q: %v", name, err)
		}
	}

	// A user edit must survive a later Install (i.e. an upgrade).
	edited := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(Path("xfce.alpine.sh"), []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	got, err := Read("xfce.alpine.sh")
	if err != nil {
		t.Fatal(err)
	}
	if got != edited {
		t.Error("Install overwrote a user-edited recipe")
	}
}

// A recipe stoat wrote and nobody touched must be refreshed when the bundled
// copy changes. This is the whole point of the manifest: before it, a shipped
// fix never reached anyone who already had the broken version: the
// xfce.cloud.yaml that produced a black screen kept being served long after
// the bundled copy was fixed.
func TestInstallRefreshesItsOwnStaleCopy(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Read("xfce.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Stand in for "an older stoat shipped this": overwrite the file AND
	// record its checksum, which is exactly the state an older release left
	// behind.
	stale := "#cloud-config\n# from an older stoat\n"
	if err := os.WriteFile(Path("xfce.cloud.yaml"), []byte(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	m := readManifest()
	m["xfce.cloud.yaml"] = sum([]byte(stale))
	if err := writeManifest(m); err != nil {
		t.Fatal(err)
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}
	got, err := Read("xfce.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != fresh {
		t.Error("Install left its own stale copy in place; a shipped recipe fix will never reach a user who already has the old one")
	}
	if _, err := os.Stat(Path("xfce.cloud.yaml.bak")); err == nil {
		t.Error("Install kept a .bak of a file it knew was its own; the backup is only for the one-time pre-manifest adoption")
	}
}

// The upgrade path: recipes on disk from before the manifest existed. There is
// no way to tell an edited one from an untouched one, so they are refreshed
// but the old contents are kept alongside.
func TestInstallAdoptsPreManifestRecipesAndKeepsABackup(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	fresh, err := Read("xfce.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}

	old := "#cloud-config\n# predates the manifest\n"
	if err := os.WriteFile(Path("xfce.cloud.yaml"), []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(Path(ManifestName)); err != nil {
		t.Fatal(err)
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}
	got, err := Read("xfce.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != fresh {
		t.Error("Install did not refresh a pre-manifest recipe")
	}
	bak, err := os.ReadFile(Path("xfce.cloud.yaml.bak"))
	if err != nil {
		t.Fatalf("Install overwrote a pre-manifest recipe without keeping a backup: %v", err)
	}
	if string(bak) != old {
		t.Errorf("backup holds %q, want the pre-manifest contents", bak)
	}

	// Adoption is once. With a manifest now on disk, a later edit is
	// recognised as an edit and left alone.
	edit := "#cloud-config\n# mine\n"
	if err := os.WriteFile(Path("xfce.cloud.yaml"), []byte(edit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("xfce.cloud.yaml"); got != edit {
		t.Error("Install reverted an edit made after adoption")
	}
	// And keeps leaving it alone: recording the bundled sum for a file it
	// did not write would make the next Install think the edit was its own.
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read("xfce.cloud.yaml"); got != edit {
		t.Error("Install reverted an edit on the second run after adoption")
	}
}

// A .bak left by adoption must not be offered as a recipe, and neither must
// the manifest.
func TestBookkeepingFilesAreNotRecipes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path("xfce.cloud.yaml.bak"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, osName := range []string{"alpine", "ubuntu", "arch"} {
		got, err := List(osName, "cloudinit")
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range got {
			if strings.HasSuffix(n, ".bak") || n == ManifestName {
				t.Errorf("List(%q) offered bookkeeping file %q", osName, n)
			}
		}
	}
	installed, err := Installed()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range installed {
		if n == ManifestName {
			t.Errorf("Installed listed %q, which is stoat's own bookkeeping", n)
		}
	}
}

func TestEmbedContainsExactlyIntendedFiles(t *testing.T) {
	// go:embed must pick up exactly the per-OS shell recipes plus the cloud
	// fragments, with no strays (recipes.go/recipes_test.go must NOT be swept up
	// by a loose embed pattern).
	entries, err := bundled.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"xfce.alpine.sh":         true,
		"xfce.ubuntu.sh":         true,
		"xfce.arch.sh":           true,
		"xfce.debian.sh":         true,
		"docker.alpine.sh":       true,
		"devtools.alpine.sh":     true,
		"tailscale.alpine.sh":    true,
		"xfce.cloud.yaml":        true,
		"xfce.fedora.cloud.yaml": true,
		"xfce.alpine.cloud.yaml": true,
		"xfce.debian.cloud.yaml": true,
		"devtools.cloud.yaml":    true,
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name()] = true
	}
	if len(got) != len(want) {
		t.Errorf("embedded files = %v, want exactly %v", got, want)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("embed missing %q", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("embed contains stray file %q", name)
		}
	}
}

func TestListFiltersByOSAndBackend(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	contains := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	cases := []struct {
		osName, backend string
		want            string
		notWant         []string
	}{
		{"ubuntu", "cloudinit", "xfce.cloud.yaml", []string{"xfce.ubuntu.sh", "xfce.alpine.sh"}},
		{"alpine", "apkovl", "xfce.alpine.sh", []string{"xfce.cloud.yaml", "xfce.ubuntu.sh"}},
		{"ubuntu", "ssh", "xfce.ubuntu.sh", []string{"xfce.cloud.yaml", "xfce.alpine.sh"}},
		{"arch", "ssh", "xfce.arch.sh", []string{"xfce.cloud.yaml", "xfce.ubuntu.sh"}},
	}
	for _, c := range cases {
		names, err := List(c.osName, c.backend)
		if err != nil {
			t.Fatal(err)
		}
		if !contains(names, c.want) {
			t.Errorf("List(%q, %q) = %v, want it to contain %q", c.osName, c.backend, names, c.want)
		}
		for _, nw := range c.notWant {
			if contains(names, nw) {
				t.Errorf("List(%q, %q) = %v, must NOT contain %q", c.osName, c.backend, names, nw)
			}
		}
	}

	// alpine is CloudRecipes-eligible for the shared set (devtools.cloud.yaml
	// installs fine on apk) and has its own xfce fragment (xfce.cloud.yaml's
	// systemctl runcmd does not work under Alpine's OpenRC), same pattern
	// as Fedora's xfce override, so it must get exactly these two entries,
	// never the shared xfce.cloud.yaml.
	alpineNames, err := List("alpine", "cloudinit")
	if err != nil {
		t.Fatalf("List(\"alpine\", \"cloudinit\"): %v", err)
	}
	if !contains(alpineNames, "devtools.cloud.yaml") {
		t.Errorf("List(\"alpine\", \"cloudinit\") = %v, want it to contain devtools.cloud.yaml", alpineNames)
	}
	if !contains(alpineNames, "xfce.alpine.cloud.yaml") {
		t.Errorf("List(\"alpine\", \"cloudinit\") = %v, want it to contain xfce.alpine.cloud.yaml", alpineNames)
	}
	if contains(alpineNames, "xfce.cloud.yaml") {
		t.Errorf("List(\"alpine\", \"cloudinit\") = %v, must NOT contain xfce.cloud.yaml, since its systemctl runcmd does not run under Alpine's OpenRC", alpineNames)
	}
	// fedora is served by a per-OS fragment rather than the shared one: it
	// has no package literally named "xfce4", so it needs the comps group
	// @xfce-desktop. It must get exactly ONE xfce entry: the
	// Fedora fragment, never the shared file, which would fail on dnf.
	names, err := List("fedora", "cloudinit")
	if err != nil {
		t.Fatalf("List(\"fedora\", \"cloudinit\"): %v", err)
	}
	if len(names) != 1 || names[0] != "xfce.fedora.cloud.yaml" {
		t.Errorf("List(\"fedora\", \"cloudinit\") = %v, want [xfce.fedora.cloud.yaml]", names)
	}

	// The converse: an OS covered by the shared fragment must NOT also be
	// offered Fedora's, or the picker shows two xfce entries and one of them
	// installs a group name apt has never heard of. Checked by content
	// rather than exact length/order, since devtools.cloud.yaml is a second
	// shared fragment now and a future one shouldn't need this test rewritten.
	// Debian is NOT in this list: it now has its own xfce.debian.cloud.yaml,
	// because Debian's cloud kernel ships no DRM drivers at all and the shared
	// fragment therefore produces a lightdm that can never start an X server.
	// Its override is asserted separately below.
	for _, os := range []string{"ubuntu", "arch"} {
		names, err := List(os, "cloudinit")
		if err != nil {
			t.Fatalf("List(%q, \"cloudinit\"): %v", os, err)
		}
		if !contains(names, "xfce.cloud.yaml") {
			t.Errorf("List(%q, \"cloudinit\") = %v, want it to contain xfce.cloud.yaml", os, names)
		}
		if contains(names, "xfce.fedora.cloud.yaml") {
			t.Errorf("List(%q, \"cloudinit\") = %v, must NOT contain xfce.fedora.cloud.yaml", os, names)
		}
	}
}

func TestShellRecipesConfigureReposBeforeInstalling(t *testing.T) {
	// Each recipe runs after boot over ssh, so it may install from the
	// network, but it must set up/refresh repositories first. Assert
	// through the real path (Install then Read) so this proves what stoat
	// actually installs for a user, not just what's in the source tree.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		repoCmd    string
		installCmd string
	}{
		{"xfce.alpine.sh", "setup-apkrepos", "apk add"},
		{"xfce.ubuntu.sh", "apt-get update", "apt-get install"},
		// arch uses a single pacman -Syu (avoids the partial-upgrade hazard
		// of a separate -Sy + -S), so repo refresh and install are the same
		// command, so repoIdx == installIdx is expected and fine here.
		{"xfce.arch.sh", "pacman -Syu", "pacman -Syu"},
	}
	for _, c := range cases {
		s, err := Read(c.name)
		if err != nil {
			t.Fatal(err)
		}
		repoIdx := strings.Index(s, c.repoCmd)
		installIdx := strings.Index(s, c.installCmd)
		if repoIdx == -1 {
			t.Errorf("%s does not configure repos with %q", c.name, c.repoCmd)
		}
		if installIdx == -1 {
			t.Errorf("%s does not install packages with %q", c.name, c.installCmd)
		}
		if repoIdx != -1 && installIdx != -1 && repoIdx > installIdx {
			t.Errorf("%s installs packages (%q) before configuring repos (%q)", c.name, c.installCmd, c.repoCmd)
		}
	}
}

func TestInstalledAlpineRecipeMatchesVirtioGPU(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("xfce.alpine.sh")
	if err != nil {
		t.Fatal(err)
	}
	// The VM boots with -vga virtio (virtio-gpu), which setup-xorg-base's
	// mesa-dri-gallium + modesetting driver already cover. xf86-video-qxl
	// is the driver for QEMU's separate QXL/SPICE adapter (-vga qxl) and
	// does not match this device.
	if strings.Contains(s, "xf86-video-qxl") {
		t.Error("xfce.alpine.sh installs xf86-video-qxl, which does not match -vga virtio (virtio-gpu)")
	}
}

func TestInstalledAlpineRecipeConfiguresAutologinAndGuardedStartx(t *testing.T) {
	// Provisioning must land the user in a desktop, not a bare console: tty1
	// needs to autologin root, and .profile needs to start X. Assert through
	// the real path (Install then Read) so this proves what stoat actually
	// ships, not just what's in the source tree.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("xfce.alpine.sh")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(s, "/etc/inittab") {
		t.Error("xfce.alpine.sh does not touch /etc/inittab to configure tty1 autologin")
	}
	// busybox getty has no -a/autologin flag; the recipe must not use it.
	if strings.Contains(s, "getty -a") {
		t.Error("xfce.alpine.sh uses `getty -a`, which busybox getty does not support")
	}

	// A recipe that stacks another `exec startx` block on every re-run is
	// the obvious regression, so the guard string must be present.
	if !strings.Contains(s, "exec startx") {
		t.Error("xfce.alpine.sh does not append a startx block to /root/.profile")
	}
	if !strings.Contains(s, "grep -q 'exec startx' /root/.profile") {
		t.Error("xfce.alpine.sh does not guard the .profile append against re-running the recipe")
	}
}

func TestShellRecipesAreHonestAboutLiveVsDiskPersistence(t *testing.T) {
	// A live root is tmpfs/overlay: whatever a recipe writes to /etc, /root,
	// or the package database is discarded on reboot. Telling a live-VM
	// user to reboot for a persistent desktop is a lie, so every ssh shell
	// recipe must branch on live vs disk instead of printing one
	// unconditional "reboot to land in xfce" message. Assert through the
	// real path (Install then Read) so this proves what stoat actually
	// ships, not just what's in the source tree.
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}

	for _, name := range allShellRecipes {
		s, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}

		// The recipe must detect live vs disk from inside the guest
		// (checking the root filesystem type is the verified mechanism)
		// rather than unconditionally assuming persistence.
		if !strings.Contains(s, "/proc/mounts") {
			t.Errorf("%s does not inspect /proc/mounts to detect a live (tmpfs/overlay) root", name)
		}
		if !strings.Contains(s, "tmpfs") {
			t.Errorf("%s does not check for a tmpfs root", name)
		}
		if !strings.Contains(s, "case ") || !strings.Contains(s, "esac") {
			t.Errorf("%s does not branch on live vs disk (no case/esac found)", name)
		}

		// The unconditional promise must be gone: it can only appear inside
		// the disk branch now, never as the recipe's only possible output.
		if strings.Count(s, "reboot the vm to land in xfce automatically") > 1 {
			t.Errorf("%s prints the disk-only reboot promise more than once", name)
		}
		if !strings.Contains(s, "reboot") || !strings.Contains(s, "NOT") {
			t.Errorf("%s does not honestly warn a live-VM user that rebooting will not bring xfce back", name)
		}
	}
}

// TestCloudFragmentsParse asserts every bundled cloud fragment against the
// contract the merger actually relies on. It parses rather than string-matches
// because string-matching is what let a real bug ship: a collapsed comment
// swallowed the `packages:` key in both fragments, leaving them invalid YAML
// with no packages at all, and `strings.Contains(s, "packages:")` still passed
// because it matched the word inside the comment. Every cloud VM's xfce recipe
// installed nothing for a full release.
func TestCloudFragmentsParse(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"xfce.cloud.yaml", "xfce.fedora.cloud.yaml"} {
		s, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(s, "#cloud-config") {
			t.Errorf("%s does not start with #cloud-config", name)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
			t.Errorf("%s is not valid YAML: %v", name, err)
			continue
		}
		pkgs, _ := doc["packages"].([]any)
		if len(pkgs) == 0 {
			t.Errorf("%s has no packages: list; the fragment installs nothing", name)
		}
		// mergeCloudRecipes is gone: fragments are handed to cloud-init
		// verbatim inside a cloud-config-archive, so a top-level key besides
		// packages/runcmd now survives rather than being silently dropped.
		// Nothing here restricts which keys a fragment may declare any more.
		// dbus-x11 is not a real Arch package, and lightdm-gtk-greeter is not
		// a real Fedora one. Checked against the parsed list, so an
		// explanatory mention in a comment is fine.
		for _, p := range pkgs {
			switch p {
			case "dbus-x11":
				if name == "xfce.cloud.yaml" {
					t.Errorf("%s installs dbus-x11, which does not exist on Arch", name)
				}
			case "lightdm-gtk-greeter":
				if name == "xfce.fedora.cloud.yaml" {
					t.Errorf("%s installs lightdm-gtk-greeter; on Fedora it is lightdm-gtk", name)
				}
			}
		}
	}
}

// TestAlpineCloudFragmentUsesOpenRCNotSystemd asserts xfce.alpine.cloud.yaml
// against the two things that made xfce.cloud.yaml (systemd) unsafe to just
// reuse for Alpine: it must not shell out to systemctl (Alpine is OpenRC,
// there is no such binary), and, because cloud-init's packages: module
// installs before runcmd runs, and a stock Alpine cloud image ships with the
// community repo (where every desktop package here lives) commented out,
// it must not rely on a packages: list at all; everything has to happen
// through runcmd instead. It also pins the dbus-before-lightdm ordering a
// real boot test caught missing on the first version of this fragment (see
// the dbus-openrc checks below): that failure produced cloud-init exit
// status "error" with no desktop and no diagnosis beyond a log file, exactly
// the class of silent failure this whole file exists to catch before a boot
// test has to.
func TestAlpineCloudFragmentUsesOpenRCNotSystemd(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("xfce.alpine.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "#cloud-config") {
		t.Error("xfce.alpine.cloud.yaml does not start with #cloud-config")
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("xfce.alpine.cloud.yaml is not valid YAML: %v", err)
	}
	// mergeCloudRecipes is gone: fragments now ride verbatim in a
	// cloud-config-archive, so a key besides packages/runcmd would survive
	// rather than being silently dropped. Nothing here restricts the keys.
	if _, ok := doc["packages"]; ok {
		t.Error("xfce.alpine.cloud.yaml declares packages:, but cloud-init installs packages: before runcmd runs, too early to have enabled the community repo the desktop packages live in")
	}
	runcmd, _ := doc["runcmd"].([]any)
	if len(runcmd) == 0 {
		t.Fatal("xfce.alpine.cloud.yaml has no runcmd: the fragment does nothing")
	}

	// Checked against the parsed runcmd items, not the raw file text, so an
	// explanatory mention of "systemctl" in a comment (as above) doesn't
	// trip this.
	var haveRCUpdate, haveXorgBase, haveDbusOpenrc bool
	var dbusStartIdx, lightdmStartIdx = -1, -1
	for i, c := range runcmd {
		cmd, _ := c.(string)
		if strings.Contains(cmd, "systemctl") {
			t.Errorf("xfce.alpine.cloud.yaml runs %q, which does not exist under Alpine's OpenRC", cmd)
		}
		if strings.Contains(cmd, "rc-update") {
			haveRCUpdate = true
		}
		if strings.Contains(cmd, "setup-xorg-base") {
			haveXorgBase = true
			if strings.Contains(cmd, "dbus-openrc") {
				haveDbusOpenrc = true
			}
		}
		if strings.Contains(cmd, "rc-service dbus start") {
			dbusStartIdx = i
		}
		if strings.Contains(cmd, "rc-service lightdm start") {
			lightdmStartIdx = i
		}
	}
	if !haveRCUpdate {
		t.Error("xfce.alpine.cloud.yaml never calls rc-update to enable lightdm under OpenRC")
	}
	// A real boot test caught this: apk's dbus package installs the daemon
	// but not its OpenRC init script (that's the separate dbus-openrc
	// subpackage), so lightdm's `need dbus` never resolved and it refused
	// to start with "dbus would not start". Pinned here so a future edit
	// can't silently drop the fix without a second boot-test round trip.
	if !haveDbusOpenrc {
		t.Error("xfce.alpine.cloud.yaml does not install dbus-openrc, so lightdm's `need dbus` has no OpenRC service to resolve to, and it will refuse to start")
	}
	if dbusStartIdx == -1 {
		t.Error("xfce.alpine.cloud.yaml never starts dbus; lightdm depends on it")
	}
	if lightdmStartIdx == -1 {
		t.Error("xfce.alpine.cloud.yaml never starts lightdm")
	}
	if dbusStartIdx != -1 && lightdmStartIdx != -1 && dbusStartIdx > lightdmStartIdx {
		t.Error("xfce.alpine.cloud.yaml starts lightdm before dbus, but lightdm needs a running dbus to start")
	}
	if !haveXorgBase {
		t.Error("xfce.alpine.cloud.yaml does not install an X server (expected via setup-xorg-base): a desktop with no X server is the black-screen failure this fragment must avoid")
	}
}

// TestDevtoolsCloudFragment parses devtools.cloud.yaml as real YAML rather
// than string-matching it. A substring check like
// strings.Contains(s, "packages:") would still pass even if a collapsed
// comment swallowed the packages: key and orphaned the list items at the
// top level: that shape is invalid YAML, and mergeCloudRecipes
// (internal/cloudinit) would silently find no packages block, so the
// fragment would install nothing with no error anywhere. Parsing catches
// what string-matching can't.
func TestDevtoolsCloudFragment(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	s, err := Read("devtools.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "#cloud-config") {
		t.Error("devtools.cloud.yaml does not start with #cloud-config")
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatalf("devtools.cloud.yaml is not valid YAML: %v", err)
	}

	pkgs, _ := doc["packages"].([]any)
	if len(pkgs) == 0 {
		t.Fatal("devtools.cloud.yaml has no packages: list; the fragment installs nothing")
	}

	// mergeCloudRecipes is gone: fragments now ride verbatim in a
	// cloud-config-archive, so nothing here restricts which top-level keys
	// a fragment may declare any more.

	want := []string{"git", "curl", "ca-certificates", "vim", "tmux", "less"}
	got := map[string]bool{}
	for _, p := range pkgs {
		if s, ok := p.(string); ok {
			got[s] = true
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("devtools.cloud.yaml does not install %q", w)
		}
	}
}

// TestListOffersDebianXfceOverSSH covers the new xfce.debian.sh recipe: it
// must be offered on the ssh backend for osName "debian" (the file naming
// contract List relies on), and not bleed into the cloud-config or ubuntu
// results.
func TestListOffersDebianXfceOverSSH(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	names, err := List("debian", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range names {
		if n == "xfce.debian.sh" {
			found = true
		}
		if n == "xfce.cloud.yaml" || n == "xfce.ubuntu.sh" {
			t.Errorf("List(\"debian\", \"ssh\") = %v, must not contain %q", names, n)
		}
	}
	if !found {
		t.Errorf("List(\"debian\", \"ssh\") = %v, want it to contain \"xfce.debian.sh\"", names)
	}
}

// TestDebianGetsItsOwnCloudXfceFragment pins that Debian is offered
// xfce.debian.cloud.yaml and NOT the shared xfce.cloud.yaml.
//
// Debian needs its own for a reason none of the other cloud images share, and
// it is not package naming: debian-13-genericcloud ships
// linux-image-cloud-amd64, whose module tree has no drivers/gpu/drm directory
// at all. With no DRM there is no /dev/dri, logind's seat0 has no graphics
// device, and lightdm waits forever for a seat that can never display.
//
// The shared fragment fails SILENTLY there, verified on a real boot:
// cloud-init reports done with errors: [], 18 xfce4 packages install,
// xserver-xorg is present, lightdm reports active, and /tmp/.X11-unix is
// empty with no Xorg process. Offering both fragments, or the shared one
// alone, would put that back.
func TestDebianGetsItsOwnCloudXfceFragment(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	names, err := List("debian", "cloudinit")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(names, "xfce.debian.cloud.yaml") {
		t.Errorf("List(debian, cloudinit) = %v, want the debian override", names)
	}
	if contains(names, "xfce.cloud.yaml") {
		t.Errorf("List(debian, cloudinit) = %v: the shared fragment must be suppressed by the override", names)
	}
}

// The debian fragment is worthless if it does not install the full kernel and
// get rid of the cloud one: installing linux-image-amd64 alongside it is not
// enough, because GRUB_DEFAULT=0 still boots the cloud flavour (observed: the
// VM came back up on the cloud kernel with both installed).
func TestDebianCloudFragmentReplacesTheCloudKernel(t *testing.T) {
	body, err := bundled.ReadFile("xfce.debian.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"linux-image-amd64", // the full flavour, with DRM
		// BOTH names: linux-image-cloud-amd64 is only a META-package, and
		// purging it alone frees 13.3 kB while leaving the real versioned
		// kernel installed, which is how the first version of this fragment
		// failed, with update-grub still finding two kernels and entry 0 still
		// the cloud one.
		`purge -y linux-image-cloud-amd64 "linux-image-$(uname -r)"`,
		"update-grub",
		"reboot", // the running cloud kernel can never load a DRM driver
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("xfce.debian.cloud.yaml is missing %q", want)
		}
	}
}

// The purge in that fragment cannot work unattended without these three lines,
// and each one is here because dropping it produced an observed failure that
// took a boot test to find. They are easy to mistake for defensive noise and
// delete, so they are pinned.
func TestDebianCloudFragmentSurvivesUnattendedRun(t *testing.T) {
	body, err := bundled.ReadFile("xfce.debian.cloud.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ want, why string }{
		{
			`linux-base/removing-running-kernel boolean false`,
			"purging the running kernel asks debconf \"Abort kernel removal?\" " +
				"whose Default is true, so without preseeding false the purge " +
				"either hangs on the dialog forever (observed: dpkg's prerm " +
				"blocked for 17 minutes) or, with DEBIAN_FRONTEND=noninteractive " +
				"alone, takes the default and aborts",
		},
		{
			`grep -q '^install ok installed' || exit 0`,
			"cloud-init runs runcmd even when its package step FAILED (observed, " +
				"twice: once a transient apt DNS failure, once a dpkg ordering " +
				"race). Without this guard the purge removes the only kernel on " +
				"the system and leaves an unbootable VM",
		},
		{
			`dpkg --configure -a`,
			"plymouth's initramfs hook copies /etc/fonts/fonts.conf while " +
				"fontconfig is still only unpacked, so the kernel postinst dies " +
				"and linux-image-amd64 is left \"install ok unpacked\" (observed). " +
				"Re-running the postinst once apt has settled fixes it",
		},
	} {
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("xfce.debian.cloud.yaml is missing %q\nit is needed because %s", tc.want, tc.why)
		}
	}
}
