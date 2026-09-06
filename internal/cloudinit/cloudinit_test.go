package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

// listOf returns the merged user-data's value for key as a list of strings.
// Every caller here asks about packages: or runcmd:, both of which cloud-init
// treats as lists of scalars.
func listOf(t *testing.T, ud, key string) []string {
	t.Helper()
	raw, ok := parseMapping(t, ud)[key]
	if !ok {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s is not a list:\n%s", key, ud)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

const testPubkey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMEJWDI8nb2ebdwSCKALxAUfgV97KKvVFxyDf+OnpgKA stoat"

func TestSeedWritesUserDataAndMetaData(t *testing.T) {
	// Seed() hard-errors without xorriso by design: a silent fallback used
	// to leave a missing xorriso as a permanent "Could not open seed.iso"
	// at qemu start. That contract has its own test below. This test only
	// checks the seed's contents, so it skips instead of failing CI on a
	// machine without the tool.
	if !haveXorriso() {
		t.Skip("xorriso not installed: skipping seed-content test")
	}

	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	v := &config.VM{
		Name: "web1", Mode: "disk", RAM: 2048, CPUs: 2,
		Dir: filepath.Join(root, "web1"),
	}

	isoPath, err := Seed(v, testPubkey, nil)
	if err != nil {
		t.Fatal(err)
	}

	seedDir := filepath.Join(v.OvlDir(), "seed")
	userData, err := os.ReadFile(filepath.Join(seedDir, "user-data"))
	if err != nil {
		t.Fatalf("reading user-data: %v", err)
	}
	ud := string(userData)

	// The seed is one merged mapping, so these are the parsed values rather
	// than the template's own quoting. yaml.Marshal emits the sudo rule as a
	// plain scalar; the value cloud-init reads is the same.
	parsed := parseMapping(t, ud)
	users, ok := parsed["users"].([]any)
	if !ok || len(users) != 1 {
		t.Fatalf("user-data does not declare exactly one user:\n%s", ud)
	}
	account := users[0].(map[string]any)
	if account["name"] != User {
		t.Errorf("user name = %v, want %q", account["name"], User)
	}
	if account["sudo"] != "ALL=(ALL) NOPASSWD:ALL" {
		t.Errorf("sudo = %v, want the passwordless rule", account["sudo"])
	}
	if account["shell"] != "/bin/bash" {
		t.Errorf("shell = %v, want /bin/bash", account["shell"])
	}
	keys, ok := account["ssh_authorized_keys"].([]any)
	if !ok || len(keys) != 1 || keys[0] != testPubkey {
		t.Errorf("authorized keys = %v, want the caller's key", account["ssh_authorized_keys"])
	}
	if parsed["ssh_pwauth"] != false {
		t.Errorf("ssh_pwauth = %v, want false", parsed["ssh_pwauth"])
	}
	if strings.Contains(ud, "- default") {
		t.Error("user-data must not include the distro default user")
	}

	metaData, err := os.ReadFile(filepath.Join(seedDir, "meta-data"))
	if err != nil {
		t.Fatalf("reading meta-data: %v", err)
	}
	md := string(metaData)
	if !strings.Contains(md, `instance-id: "stoat-web1"`) {
		t.Errorf("meta-data missing quoted instance-id: %q", md)
	}
	if !strings.Contains(md, `local-hostname: "web1"`) {
		t.Errorf("meta-data missing quoted local-hostname: %q", md)
	}

	wantISO := filepath.Join(v.OvlDir(), "seed.iso")
	if isoPath != wantISO {
		t.Errorf("Seed returned %q, want %q", isoPath, wantISO)
	}

	f, err := os.Open(isoPath)
	if err != nil {
		t.Fatalf("seed.iso not written: %v", err)
	}
	defer func() { _ = f.Close() }()

	label := make([]byte, 32)
	if _, err := f.ReadAt(label, 0x8028); err != nil {
		t.Fatalf("reading ISO9660 volume label: %v", err)
	}
	if got := strings.TrimSpace(string(label)); got != "CIDATA" {
		t.Errorf("ISO9660 volume label = %q, want CIDATA", got)
	}
}

func TestHaveXorriso(t *testing.T) {
	// Just exercise the function; result depends on the environment.
	_ = haveXorriso()
}

func TestHaveCloudInit(t *testing.T) {
	// Just exercise the function; result depends on the environment.
	_ = haveCloudInit()
}

// TestValidateFragmentDegradesWithoutCloudInit pins guest-subsystem.md §10:
// cloud-init schema may be absent (Arch does not install it by default).
// Absence must show up as "not checked": annotated == "" and err == nil.
// It must never read as "assumed valid", and it must never block recipe
// selection just because the host lacks the tool.
func TestValidateFragmentDegradesWithoutCloudInit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	annotated, err := ValidateFragment("#cloud-config\npackages:\n  - git\n")
	if err != nil {
		t.Fatalf("want nil error when cloud-init is absent, got %v", err)
	}
	if annotated != "" {
		t.Errorf("want empty (unchecked) output when cloud-init is absent, got %q", annotated)
	}
}

// TestSeedNoRecipesKeepsTheProvenAccount guards the hardware-proven baseline.
// A VM with no cloud recipes must declare the same account that was
// hand-verified against a real Ubuntu 24.04 boot (see
// .cloudinit-test/seed/user-data): one user named stoat, passwordless sudo,
// the guest's shell, the caller's key, and no distro default user.
//
// The seed is one merged mapping now, so this compares the parsed account
// rather than the file's bytes. mergeDocs reorders keys and drops comments.
func TestSeedNoRecipesKeepsTheProvenAccount(t *testing.T) {
	// Seed() hard-errors without xorriso by design: a silent fallback used
	// to leave a missing xorriso as a permanent "Could not open seed.iso"
	// at qemu start. That contract has its own test below. This test only
	// checks the seed's contents, so it skips instead of failing CI on a
	// machine without the tool.
	if !haveXorriso() {
		t.Skip("xorriso not installed: skipping seed-content test")
	}

	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	v := &config.VM{
		Name: "web1", Mode: "cloud", RAM: 2048, CPUs: 2,
		Dir: filepath.Join(root, "web1"),
	}
	if _, err := Seed(v, testPubkey, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(v.OvlDir(), "seed", "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseMapping(t, string(got))
	users, ok := parsed["users"].([]any)
	if !ok || len(users) != 1 {
		t.Fatalf("want exactly one declared user:\n%s", got)
	}
	account, ok := users[0].(map[string]any)
	if !ok {
		t.Fatalf("the user entry is not a mapping:\n%s", got)
	}
	for key, want := range map[string]any{
		"name":  User,
		"sudo":  "ALL=(ALL) NOPASSWD:ALL",
		"shell": "/bin/bash",
	} {
		if account[key] != want {
			t.Errorf("user %s = %v, want %v", key, account[key], want)
		}
	}
	keys, ok := account["ssh_authorized_keys"].([]any)
	if !ok || len(keys) != 1 || keys[0] != testPubkey {
		t.Errorf("authorized keys = %v, want exactly the caller's key", account["ssh_authorized_keys"])
	}
	if parsed["ssh_pwauth"] != false {
		t.Errorf("ssh_pwauth = %v, want false", parsed["ssh_pwauth"])
	}
	if strings.Contains(string(got), "- default") {
		t.Errorf("user-data includes the distro default user:\n%s", got)
	}
	if _, ok := parsed["packages"]; ok {
		t.Errorf("a VM with no recipes declared packages:\n%s", got)
	}
}

// TestSeedMergesCloudRecipe is the C1 regression. A cloud VM with a recipe
// selected keeps the proven account and gains the recipe's own keys.
func TestSeedMergesCloudRecipe(t *testing.T) {
	// Seed() hard-errors without xorriso by design: a silent fallback used
	// to leave a missing xorriso as a permanent "Could not open seed.iso"
	// at qemu start. That contract has its own test below. This test only
	// checks the seed's contents, so it skips instead of failing CI on a
	// machine without the tool.
	if !haveXorriso() {
		t.Skip("xorriso not installed: skipping seed-content test")
	}

	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	v := &config.VM{
		Name: "web1", Mode: "cloud", RAM: 2048, CPUs: 2,
		Dir: filepath.Join(root, "web1"),
	}
	fragment := "#cloud-config\npackages:\n  - xfce4\n  - xfce4-terminal\n\nruncmd:\n  - systemctl enable dbus\n"

	if _, err := Seed(v, testPubkey, []string{fragment}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(v.OvlDir(), "seed", "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	ud := string(got)
	users, ok := parseMapping(t, ud)["users"].([]any)
	if !ok || len(users) != 1 {
		t.Fatalf("the recipe displaced the proven account:\n%s", ud)
	}
	if name := users[0].(map[string]any)["name"]; name != User {
		t.Errorf("account name = %v, want %q", name, User)
	}
	for _, want := range []string{"xfce4", "xfce4-terminal"} {
		if !slices.Contains(listOf(t, ud, "packages"), want) {
			t.Errorf("packages lost %q:\n%s", want, ud)
		}
	}
	if !slices.Contains(listOf(t, ud, "runcmd"), "systemctl enable dbus") {
		t.Errorf("runcmd lost the recipe's entry:\n%s", ud)
	}
}

func TestSeedSecretArtifactsArePrivate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	bin := t.TempDir()
	// The stand-in deliberately unlinks and recreates the ISO, inheriting the
	// caller's umask. Seed must protect the replacement inode before xorriso
	// writes bytes.
	modeFile := filepath.Join(root, "xorriso-create-mode")
	modeFileQ := shellQuoteCloudinitTest(modeFile)
	xorriso := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then out=$2; shift 2; else shift; fi\ndone\nrm -f \"$out\"\n: > \"$out\"\nstat -c '%a' \"$out\" > " + modeFileQ + "\nprintf 'private seed' > \"$out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "xorriso"), []byte(xorriso), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	const sentinel = "cloud-secret-value"
	v := &config.VM{
		Name: "cloudy", Mode: "cloud", OS: "ubuntu", Dir: filepath.Join(root, "cloudy"),
	}
	if _, err := Seed(v, testPubkey, []string{"#cloud-config\nruncmd:\n  - echo " + sentinel + "\n"}); err != nil {
		t.Fatal(err)
	}
	createdMode, err := os.ReadFile(modeFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(createdMode)) != "600" {
		t.Fatalf("xorriso replacement mode before payload = %q, want 600", createdMode)
	}
	seedDir := filepath.Join(v.OvlDir(), "seed")
	for _, item := range []struct {
		path string
		want os.FileMode
	}{
		{seedDir, 0o700},
		{filepath.Join(seedDir, "user-data"), 0o600},
		{filepath.Join(v.OvlDir(), "seed.iso"), 0o600},
	} {
		info, err := os.Stat(item.path)
		if err != nil {
			t.Fatalf("stat %s: %v", item.path, err)
		}
		if got := info.Mode().Perm(); got != item.want {
			t.Errorf("%s mode = %#o, want %#o", item.path, got, item.want)
		}
	}
	b, err := os.ReadFile(filepath.Join(seedDir, "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), sentinel) {
		t.Fatal("private user-data seed lost the required secret-bearing recipe")
	}
}

func shellQuoteCloudinitTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// NoCloud matches "#cloud-config" on the first line, verbatim, to parse the
// payload at all. cloud-init 24.4 on AlmaLinux and Rocky also fails on a
// top-level list, which is what the cloud-config-archive this package used to
// emit was; see mergeDocs.
func TestSeedHeaderIsFirstLine(t *testing.T) {
	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first := strings.SplitN(ud, "\n", 2)[0]; first != "#cloud-config" {
		t.Errorf("first line = %q, want %q", first, "#cloud-config")
	}
	if strings.Contains(ud, "#cloud-config-archive") {
		t.Errorf("user-data is still an archive:\n%s", ud)
	}
}

// Two fragments that both declare packages: must both survive. cloud-init's
// own default merge is first-wins, which is why stoat used to declare
// merge_how; mergeDocs now appends before cloud-init ever sees the file.
func TestBothPackagesFragmentsSurvive(t *testing.T) {
	fragA := "#cloud-config\npackages:\n  - xfce4\n"
	fragB := "#cloud-config\npackages:\n  - docker\n"

	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, []string{fragA, fragB})
	if err != nil {
		t.Fatal(err)
	}
	packages := listOf(t, ud, "packages")
	for _, want := range []string{"xfce4", "docker"} {
		if !slices.Contains(packages, want) {
			t.Errorf("packages = %v, missing %q", packages, want)
		}
	}
	if users, ok := parseMapping(t, ud)["users"].([]any); !ok || len(users) != 1 {
		t.Errorf("the fragments displaced the account:\n%s", ud)
	}
}

// A fragment key this package knows nothing about must survive. The old
// mergeCloudRecipes understood packages: and runcmd: only, and silently
// dropped everything else (guest-subsystem.md §1c).
func TestWriteFilesSurvives(t *testing.T) {
	fragment := "#cloud-config\nwrite_files:\n  - path: /etc/motd\n    content: hello\n"

	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, []string{fragment})
	if err != nil {
		t.Fatal(err)
	}
	files, ok := parseMapping(t, ud)["write_files"].([]any)
	if !ok || len(files) != 1 {
		t.Fatalf("write_files was dropped:\n%s", ud)
	}
	if path := files[0].(map[string]any)["path"]; path != "/etc/motd" {
		t.Errorf("write_files path = %v, want /etc/motd", path)
	}
}

// TestSeedErrorsWithoutXorriso is the I1 regression. Seed must hard-error,
// never fall back silently to the seed directory. That dead vvfat fallback
// used to turn a missing xorriso into a permanent, silent "Could not open
// seed.iso" failure at qemu start: ensureCloudOverlay discards Seed's return
// and never re-seeds once the overlay exists.
func TestSeedErrorsWithoutXorriso(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	// An empty PATH guarantees exec.LookPath("xorriso") fails, regardless of
	// whether xorriso happens to be installed on the machine running tests.
	t.Setenv("PATH", t.TempDir())

	v := &config.VM{
		Name: "web1", Mode: "cloud", RAM: 2048, CPUs: 2,
		Dir: filepath.Join(root, "web1"),
	}
	_, err := Seed(v, testPubkey, nil)
	if err == nil {
		t.Fatal("expected an error when xorriso is unavailable, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(v.OvlDir(), "seed.iso")); statErr == nil {
		t.Error("seed.iso must not exist when Seed errored")
	}
}

// TestSeedUserMatchesUser pins the exported User constant to the account the
// template actually creates. The two literals must agree: the TUI records
// User on the VM as its SSH user, and sshx defaults an empty user to root,
// which cloud images lock. If they drift, every cloud VM connects as an
// account that does not exist, and nothing else in the suite catches it.
func TestSeedUserMatchesUser(t *testing.T) {
	if !strings.Contains(userDataTemplate, "- name: "+User) {
		t.Errorf("userDataTemplate does not create the account named by User (%q)", User)
	}
}

// Alpine ships no bash. cloud-init's user module fails on a nonexistent
// shell: the account is never created and the key never lands. The symptom
// is "Permission denied (publickey)" forever, not a fallback shell.
// Boot-tested against generic_alpine-3.24.1-x86_64-bios-cloudinit-r0.qcow2:
// /bin/bash refused every connection, /bin/ash connected on the first try.
func TestSeedUsesTheGuestsOwnShell(t *testing.T) {
	for _, tc := range []struct {
		os, want, notWant string
	}{
		{"alpine", "/bin/ash", "/bin/bash"},
		{"ubuntu", "/bin/bash", ""},
		{"fedora", "/bin/bash", ""},
		{"debian", "/bin/bash", ""},
		{"", "/bin/bash", ""}, // unknown OS keeps the old default
	} {
		v := &config.VM{Name: "vm", OS: tc.os}
		got, err := userData(v, "ssh-ed25519 AAAA test", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "shell: "+tc.want) {
			t.Errorf("os=%q: want shell %s, got:\n%s", tc.os, tc.want, got)
		}
		if tc.notWant != "" && strings.Contains(got, "shell: "+tc.notWant) {
			t.Errorf("os=%q: still asks for %s, which does not exist there", tc.os, tc.notWant)
		}
	}
}

// The sudo: key writes a sudoers fragment. On Alpine the sudo binary is not
// installed (doas is), so the fragment points at nothing and every recipe
// that escalates fails. Verified on the booted guest: command -v sudo was
// empty, command -v doas was /usr/bin/doas.
func TestSeedInstallsSudoWhereItIsMissing(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	got, err := userData(v, "ssh-ed25519 AAAA test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "sudo") {
		t.Fatalf("alpine seed does not provide for sudo at all:\n%s", got)
	}
	if !strings.Contains(got, "packages:") {
		t.Errorf("alpine seed never installs the sudo the sudoers fragment needs:\n%s", got)
	}

	// Ubuntu already ships sudo; adding a packages block for it is noise.
	u, err := userData(&config.VM{Name: "vm", OS: "ubuntu"}, "ssh-ed25519 AAAA test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u, "packages:") && strings.Contains(u, "sudo") {
		t.Errorf("ubuntu seed installs sudo it already has:\n%s", u)
	}
}

// debian's cloud kernel has no 9p module, so the seed omits the mounts
// document. A present-but-unmountable 9p entry made cloud-init report the
// whole seed as errored on every boot. A 9p-capable OS still gets the mounts.
func TestSeedSkipsMountsOnDebian(t *testing.T) {
	deb, err := userData(&config.VM{Name: "vm", OS: "debian"}, testPubkey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(deb, "mounts:") {
		t.Errorf("debian seed carries a 9p mounts document:\n%s", deb)
	}

	arch, err := userData(&config.VM{Name: "vm", OS: "arch"}, testPubkey, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(arch, "mounts:") {
		t.Errorf("arch seed is missing its 9p mounts document:\n%s", arch)
	}
}

// cloud-init 24.4 as shipped by AlmaLinux 9 and Rocky 9 calls .get() on the
// parsed user-data before it checks the type, so a top-level list raises
// AttributeError, init-local fails, and `cloud-init status` reports error for
// the life of the VM. Upstream added the isinstance guard after that build.
// Every seed is a mapping now, with or without recipes.
func TestSeedIsAlwaysAMapping(t *testing.T) {
	for _, osName := range []string{"almalinux", "rocky", "opensuse", "debian", "ubuntu", "alpine"} {
		for _, recipes := range [][]string{nil, {"#cloud-config\npackages:\n  - git\n"}} {
			ud, err := userData(&config.VM{Name: "vm", OS: osName}, testPubkey, recipes)
			if err != nil {
				t.Fatalf("%s: %v", osName, err)
			}
			if _, ok := parseMapping(t, ud)["users"]; !ok {
				t.Errorf("%s with %d recipes lost its users block:\n%s", osName, len(recipes), ud)
			}
		}
	}
}

// A recipe with packages must reach a guest whose cloud-init cannot read an
// archive. This is what #93 unblocks for #85.
func TestSeedCarriesRecipesOnAlmaLinux(t *testing.T) {
	ud, err := userData(&config.VM{Name: "vm", OS: "almalinux"}, testPubkey, []string{"#cloud-config\npackages:\n  - git\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(listOf(t, ud, "packages"), "git") {
		t.Errorf("the recipe's package was dropped:\n%s", ud)
	}
}

// Recipe fragments must still merge after the base block, whatever the OS.
func TestSeedStillMergesRecipesOnAlpine(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	got, err := userData(v, "ssh-ed25519 AAAA test", []string{"packages:\n  - git\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "git") {
		t.Errorf("recipe package was dropped:\n%s", got)
	}
}

// Alpine's own extra-packages fragment declares sudo, and a recipe's fragment
// declares git and tmux. Both must end up in the one packages: list. Alpine's
// sudo losing to a recipe means every escalating recipe on that guest fails,
// since the users: block writes a sudoers fragment for a binary that is not
// installed.
func TestSeedMergesAlpineSudoWithRecipePackages(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	fragment := "packages:\n  - git\n  - tmux\n\nruncmd:\n  - echo hi\n"
	got, err := userData(v, "ssh-ed25519 AAAA test", []string{fragment})
	if err != nil {
		t.Fatal(err)
	}

	packages := listOf(t, got, "packages")
	for _, want := range []string{"sudo", "git", "tmux"} {
		if !slices.Contains(packages, want) {
			t.Errorf("packages = %v, missing %q", packages, want)
		}
	}
	if !slices.Contains(listOf(t, got, "runcmd"), "echo hi") {
		t.Errorf("runcmd lost the recipe's entry:\n%s", got)
	}
}

// The registry is now the authority, and the seed must agree with it for
// every known OS. A drift here is the original bug returning.
func TestSeedShellMatchesTheRegistry(t *testing.T) {
	for _, o := range guest.All() {
		got, err := userData(&config.VM{Name: "vm", OS: o.Name}, "ssh-ed25519 AAAA k", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "shell: "+o.Shell) {
			t.Errorf("%s: seed does not use the registry's shell %q:\n%s", o.Name, o.Shell, got)
		}
	}
}

// An unknown or empty OS keeps the previous fallback. A BYO image can have no
// OS at all, and every image stoat supports except Alpine ships bash.
func TestSeedFallsBackToBashForAnUnknownOS(t *testing.T) {
	for _, osName := range []string{"", "plan9"} {
		got, err := userData(&config.VM{Name: "vm", OS: osName}, "ssh-ed25519 AAAA k", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "shell: /bin/bash") {
			t.Errorf("os=%q lost the bash fallback:\n%s", osName, got)
		}
	}
}

// SkipShares must read the flag from the guest file, not recognise debian by
// name: any future 9p-less guest sets backend.cloudinit.skip_9p and needs no
// Go change.
func TestSkipSharesReadsGuestFlag(t *testing.T) {
	if !SkipShares("debian") {
		t.Error("debian's guest.toml sets skip_9p; SkipShares must report true")
	}
	if SkipShares("fedora") {
		t.Error("fedora keeps the 9p mounts")
	}
	if SkipShares("plan9") {
		t.Error("an unknown guest must not skip shares")
	}
}
