package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

const testPubkey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMEJWDI8nb2ebdwSCKALxAUfgV97KKvVFxyDf+OnpgKA stoat"

func TestSeedWritesUserDataAndMetaData(t *testing.T) {
	// Seed() hard-errors without xorriso, on purpose — a silent fallback used
	// to make a missing xorriso a permanent "Could not open seed.iso" at qemu
	// start. That contract has its own test below; this one is about the
	// seed's CONTENTS, so it skips rather than turning CI permanently red on
	// a machine that simply hasn't got the tool.
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

	if !strings.HasPrefix(ud, "#cloud-config\n") {
		t.Errorf("user-data does not start with #cloud-config: %q", ud)
	}
	if !strings.Contains(ud, testPubkey) {
		t.Error("user-data missing the pubkey")
	}
	if !strings.Contains(ud, "name: stoat") {
		t.Error("user-data missing the stoat user")
	}
	if !strings.Contains(ud, `sudo: "ALL=(ALL) NOPASSWD:ALL"`) {
		t.Error("user-data missing quoted sudo directive")
	}
	if !strings.Contains(ud, "shell: /bin/bash") {
		t.Error("user-data missing shell directive")
	}
	if !strings.Contains(ud, "ssh_pwauth: false") {
		t.Error("user-data missing ssh_pwauth: false")
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
	defer f.Close()

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

// TestSeedNoRecipesByteIdentical guards the hardware-proven baseline: a VM
// with no cloud recipes must get exactly the same users:/ssh_pwauth: body
// that was hand-verified against a real Ubuntu 24.04 boot (see
// .cloudinit-test/seed/user-data), with nothing appended.
func TestSeedNoRecipesByteIdentical(t *testing.T) {
	// Seed() hard-errors without xorriso, on purpose — a silent fallback used
	// to make a missing xorriso a permanent "Could not open seed.iso" at qemu
	// start. That contract has its own test below; this one is about the
	// seed's CONTENTS, so it skips rather than turning CI permanently red on
	// a machine that simply hasn't got the tool.
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
	want := fmt.Sprintf(userDataTemplate, "/bin/bash", testPubkey, "")
	if string(got) != want {
		t.Errorf("no-recipe user-data changed from the proven baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestSeedMergesCloudRecipe is the C1 regression: a cloud VM with a recipe
// selected must produce ONE valid #cloud-config document that still
// contains the proven users: block verbatim, plus the recipe's merged
// packages.
func TestSeedMergesCloudRecipe(t *testing.T) {
	// Seed() hard-errors without xorriso, on purpose — a silent fallback used
	// to make a missing xorriso a permanent "Could not open seed.iso" at qemu
	// start. That contract has its own test below; this one is about the
	// seed's CONTENTS, so it skips rather than turning CI permanently red on
	// a machine that simply hasn't got the tool.
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

	provenUsersBlock := fmt.Sprintf(userDataTemplate, "/bin/bash", testPubkey, "")
	if !strings.Contains(ud, provenUsersBlock) {
		t.Errorf("merged user-data does not contain the proven users block verbatim:\n%s", ud)
	}
	if strings.Count(ud, "#cloud-config") != 1 {
		t.Errorf("merged user-data must be a single #cloud-config document:\n%s", ud)
	}
	if !strings.Contains(ud, "  - xfce4\n") {
		t.Errorf("merged user-data missing xfce4 package:\n%s", ud)
	}
	if !strings.Contains(ud, "systemctl enable dbus") {
		t.Errorf("merged user-data missing runcmd entry:\n%s", ud)
	}
}

// TestSeedErrorsWithoutXorriso is the I1 regression: Seed must hard-error,
// never silently fall back to handing back the seed directory (that dead
// vvfat fallback used to make a missing xorriso a permanent, silent
// "Could not open seed.iso" failure at qemu start, since ensureCloudOverlay
// discards Seed's return and never re-seeds once the overlay exists).
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
// template actually creates. They are two literals that must agree: the TUI
// records User on the VM as its SSH user, and sshx defaults an empty one to
// root — which cloud images lock. If they drift, every cloud VM connects as
// an account that does not exist, and nothing else in the suite would notice.
func TestSeedUserMatchesUser(t *testing.T) {
	if !strings.Contains(userDataTemplate, "- name: "+User) {
		t.Errorf("userDataTemplate does not create the account named by User (%q)", User)
	}
}

// Alpine ships no bash. cloud-init's user module FAILS on a nonexistent
// shell, so the account is never created and the key never lands -- the
// symptom is "Permission denied (publickey)" forever, not a fallback shell.
// Boot-tested against generic_alpine-3.24.1-x86_64-bios-cloudinit-r0.qcow2:
// /bin/bash refused every connection, /bin/ash connected first try.
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
		got := userData(v, "ssh-ed25519 AAAA test", nil)
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
	got := userData(v, "ssh-ed25519 AAAA test", nil)
	if !strings.Contains(got, "sudo") {
		t.Fatalf("alpine seed does not provide for sudo at all:\n%s", got)
	}
	if !strings.Contains(got, "packages:") {
		t.Errorf("alpine seed never installs the sudo the sudoers fragment needs:\n%s", got)
	}

	// Ubuntu already ships sudo; adding a packages block for it is noise.
	u := userData(&config.VM{Name: "vm", OS: "ubuntu"}, "ssh-ed25519 AAAA test", nil)
	if strings.Contains(u, "packages:") && strings.Contains(u, "sudo") {
		t.Errorf("ubuntu seed installs sudo it already has:\n%s", u)
	}
}

// Recipe fragments must still merge after the base block, whatever the OS --
// this is the existing contract and the reason mergeCloudRecipes exists.
func TestSeedStillMergesRecipesOnAlpine(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	got := userData(v, "ssh-ed25519 AAAA test", []string{"packages:\n  - git\n"})
	if !strings.Contains(got, "git") {
		t.Errorf("recipe package was dropped:\n%s", got)
	}
}

// countTopLevelKey counts line-anchored occurrences of "key:" -- i.e. lines
// that are NOT indented -- so it doesn't also match "  - packages:" style
// indented text or substrings elsewhere in the document.
func countTopLevelKey(doc, key string) int {
	n := 0
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, key+":") {
			n++
		}
	}
	return n
}

// Two top-level packages: keys is valid YAML -- cloud-init's parser just
// takes the last one and silently discards the first. If extraPackages(v.OS)
// were ever concatenated onto the base block as raw text ahead of
// mergeCloudRecipes(recipeBodies), instead of being merged INTO it, an
// Alpine VM with a recipe that also declares packages: would end up with two
// competing packages: keys -- the recipe's winning, Alpine's sudo silently
// vanishing, and every recipe that escalates via sudo failing on a guest
// that never got it. This pins the merge stays single-key.
func TestSeedMergesAlpineSudoWithRecipePackages(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	fragment := "packages:\n  - git\n  - tmux\n\nruncmd:\n  - echo hi\n"
	got := userData(v, "ssh-ed25519 AAAA test", []string{fragment})

	if n := countTopLevelKey(got, "packages"); n != 1 {
		t.Errorf("want exactly one top-level packages: key, got %d:\n%s", n, got)
	}
	for _, want := range []string{"sudo", "git", "tmux"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged packages missing %q:\n%s", want, got)
		}
	}
}
