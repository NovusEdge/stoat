package cloudinit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"gopkg.in/yaml.v3"
)

// unpackArchive strips the required "#cloud-config-archive" header and
// unmarshals the rest as the YAML list of {type, content} documents, so
// tests can inspect individual documents instead of grepping the whole
// rendered file.

// withoutMounts returns the archive's documents minus the 9p mounts document,
// and fails if there isn't exactly one. Every VM gets a mounts document, so
// tests about the base and recipe documents filter it out instead of counting
// around it.
func withoutMounts(t *testing.T, ud string) []archiveDoc {
	t.Helper()
	var rest []archiveDoc
	found := 0
	for _, d := range unpackArchive(t, ud) {
		if strings.Contains(d.Content, "mounts:") {
			found++
			continue
		}
		rest = append(rest, d)
	}
	if found != 1 {
		t.Fatalf("want exactly one mounts document, got %d:\n%s", found, ud)
	}
	return rest
}

func unpackArchive(t *testing.T, ud string) []archiveDoc {
	t.Helper()
	const header = "#cloud-config-archive\n"
	if !strings.HasPrefix(ud, header) {
		t.Fatalf("user-data does not start with %q:\n%s", header, ud)
	}
	var docs []archiveDoc
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(ud, header)), &docs); err != nil {
		t.Fatalf("user-data is not a valid cloud-config-archive: %v\n%s", err, ud)
	}
	return docs
}

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

	docs := withoutMounts(t, ud)
	if len(docs) != 1 {
		t.Fatalf("want exactly one document (no recipes, no extra packages), got %d", len(docs))
	}
	if docs[0].Type != "text/cloud-config" {
		t.Errorf("document type = %q, want text/cloud-config", docs[0].Type)
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

func TestHaveCloudInit(t *testing.T) {
	// Just exercise the function; result depends on the environment.
	_ = haveCloudInit()
}

// TestValidateFragmentDegradesWithoutCloudInit is the guest-subsystem.md §10
// requirement stated directly: cloud-init schema may be absent (Arch does
// not install it by default), and that must show up as "not checked" --
// annotated == "" and err == nil -- never as "assumed valid" (which would
// need a non-nil, affirmatively-passing result) and never as a hard error
// that would block recipe selection just because the host lacks the tool.
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

// TestSeedNoRecipesByteIdentical guards the hardware-proven baseline: a VM
// with no cloud recipes must get exactly the same users:/ssh_pwauth: body
// that was hand-verified against a real Ubuntu 24.04 boot (see
// .cloudinit-test/seed/user-data), with nothing appended -- now carried
// as the sole document's content inside the cloud-config-archive, plus the
// merge_how directive every document gets (see withMergeHow).
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
	docs := withoutMounts(t, string(got))
	if len(docs) != 1 {
		t.Fatalf("want exactly one document (no recipes, no extra packages), got %d", len(docs))
	}

	want := withMergeHow(fmt.Sprintf(userDataTemplate, "/bin/bash", testPubkey, ""))
	if docs[0].Content != want {
		t.Errorf("no-recipe user-data changed from the proven baseline:\ngot:\n%s\nwant:\n%s", docs[0].Content, want)
	}
}

// TestSeedMergesCloudRecipe is the C1 regression: a cloud VM with a recipe
// selected must produce a cloud-config-archive whose first document still
// contains the proven users: block verbatim (plus the merge_how directive),
// and whose second document is the recipe's fragment, byte-for-byte, plus
// its own directive.
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
	docs := withoutMounts(t, string(got))
	if len(docs) != 2 {
		t.Fatalf("want two documents (base + one recipe), got %d:\n%s", len(docs), got)
	}

	wantBase := withMergeHow(fmt.Sprintf(userDataTemplate, "/bin/bash", testPubkey, ""))
	if docs[0].Content != wantBase {
		t.Errorf("first document is not the proven users block:\ngot:\n%s\nwant:\n%s", docs[0].Content, wantBase)
	}

	wantFragment := withMergeHow(fragment)
	if docs[1].Content != wantFragment {
		t.Errorf("second document is not the recipe fragment, verbatim plus its directive:\ngot:\n%s\nwant:\n%s", docs[1].Content, wantFragment)
	}
	if !strings.Contains(docs[1].Content, "  - xfce4\n") {
		t.Errorf("recipe document missing xfce4 package:\n%s", docs[1].Content)
	}
	if !strings.Contains(docs[1].Content, "systemctl enable dbus") {
		t.Errorf("recipe document missing runcmd entry:\n%s", docs[1].Content)
	}
}

// TestSeedArchiveHeaderIsFirstLine pins the exact requirement NoCloud checks
// to recognise a cloud-config-archive: "#cloud-config-archive" must be the
// very first line of the file, verbatim (see nocloud format-detection --
// the same shape as the "#cloud-config" match this package already relied
// on for the pre-archive format).
func TestSeedArchiveHeaderIsFirstLine(t *testing.T) {
	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(ud, "\n", 2)
	if lines[0] != "#cloud-config-archive" {
		t.Errorf("first line = %q, want %q", lines[0], "#cloud-config-archive")
	}
}

// TestArchiveBothPackagesFragmentsSurvive is the required regression for the
// merge fix: two fragments that BOTH declare packages: must both survive as
// distinct documents, each carrying the explicit merge_how directive that
// makes cloud-init append rather than let the second one silently lose to
// cloud-init's default no_replace=True (the "first wins" bug this change
// fixes -- see guest-subsystem.md §6). This package no longer parses
// packages: out of fragments at all -- proving survival here means proving
// the fragment bodies are carried through unparsed AND each is tagged with
// the directive that makes cloud-init's own merge keep both, rather than
// asserting anything about what cloud-init would do (that needs a real
// boot).
func TestArchiveBothPackagesFragmentsSurvive(t *testing.T) {
	fragA := "#cloud-config\npackages:\n  - xfce4\n"
	fragB := "#cloud-config\npackages:\n  - docker\n"

	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, []string{fragA, fragB})
	if err != nil {
		t.Fatal(err)
	}
	docs := withoutMounts(t, ud)
	if len(docs) != 3 {
		t.Fatalf("want 3 documents (base + 2 fragments), got %d:\n%s", len(docs), ud)
	}

	for i, want := range []string{
		withMergeHow(fmt.Sprintf(userDataTemplate, "/bin/bash", testPubkey, "")),
		withMergeHow(fragA),
		withMergeHow(fragB),
	} {
		if docs[i].Content != want {
			t.Errorf("document %d:\ngot:\n%s\nwant:\n%s", i, docs[i].Content, want)
		}
	}

	// Every document except conceivably the last carries merge_how -- and
	// since this package can't know which fragment a caller passes last,
	// every one of them does (see withMergeHow). Pin that directly: this is
	// what turns "first wins" into "both survive" on a real boot.
	directive := fmt.Sprintf("merge_how: %q", mergeHow)
	for i, d := range docs {
		if !strings.Contains(d.Content, directive) {
			t.Errorf("document %d missing merge_how directive:\n%s", i, d.Content)
		}
	}
	if !strings.Contains(docs[1].Content, "xfce4") {
		t.Error("fragment A's package was dropped")
	}
	if !strings.Contains(docs[2].Content, "docker") {
		t.Error("fragment B's package was dropped")
	}
}

// TestArchiveWriteFilesSurvives is the write_files: regression this whole
// change exists to fix: mergeCloudRecipes used to understand only
// packages:/runcmd: and silently drop everything else (guest-subsystem.md
// §1c). Since fragments are now handed to cloud-init verbatim, any key
// survives.
func TestArchiveWriteFilesSurvives(t *testing.T) {
	fragment := "#cloud-config\nwrite_files:\n  - path: /etc/motd\n    content: hello\n"

	ud, err := userData(&config.VM{Name: "vm"}, testPubkey, []string{fragment})
	if err != nil {
		t.Fatal(err)
	}
	docs := withoutMounts(t, ud)
	if len(docs) != 2 {
		t.Fatalf("want 2 documents (base + 1 fragment), got %d:\n%s", len(docs), ud)
	}
	if !strings.Contains(docs[1].Content, "write_files:") {
		t.Errorf("write_files: was dropped:\n%s", docs[1].Content)
	}
	if !strings.Contains(docs[1].Content, "/etc/motd") {
		t.Errorf("write_files: entry was dropped:\n%s", docs[1].Content)
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

// Recipe fragments must still merge after the base block, whatever the OS --
// this is the existing contract and the reason the cloud-config-archive
// exists (see buildArchive).
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

// Alpine's own extra-packages fragment (sudo) and a recipe's fragment
// (git, tmux) both declare packages: -- they now live in separate archive
// documents rather than being spliced into one packages: list by hand, and
// each carries merge_how so cloud-init appends rather than lets the later
// one win. This is the same regression TestArchiveBothPackagesFragmentsSurvive
// covers generically; this one pins it specifically for the extraPackages
// case, since that's a document this package builds itself, not one it was
// merely handed.
func TestSeedMergesAlpineSudoWithRecipePackages(t *testing.T) {
	v := &config.VM{Name: "vm", OS: "alpine"}
	fragment := "packages:\n  - git\n  - tmux\n\nruncmd:\n  - echo hi\n"
	got, err := userData(v, "ssh-ed25519 AAAA test", []string{fragment})
	if err != nil {
		t.Fatal(err)
	}

	docs := withoutMounts(t, got)
	if len(docs) != 3 {
		t.Fatalf("want 3 documents (base + alpine's extraPackages + recipe), got %d:\n%s", len(docs), got)
	}
	if !strings.Contains(docs[1].Content, "sudo") {
		t.Errorf("alpine's extraPackages document missing sudo:\n%s", docs[1].Content)
	}
	for _, want := range []string{"git", "tmux"} {
		if !strings.Contains(docs[2].Content, want) {
			t.Errorf("recipe document missing %q:\n%s", want, docs[2].Content)
		}
	}
	// Both packages-declaring documents (extraPackages and the recipe) must
	// carry merge_how, or the recipe's packages would silently overwrite
	// alpine's sudo instead of appending to it.
	directive := fmt.Sprintf("merge_how: %q", mergeHow)
	if !strings.Contains(docs[1].Content, directive) {
		t.Errorf("extraPackages document missing merge_how:\n%s", docs[1].Content)
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
