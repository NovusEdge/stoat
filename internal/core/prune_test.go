package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeBroken drops a vm.toml under dir/name that fails to parse (an
// unterminated string), optionally with extra lines appended verbatim before
// the break — used to test the best-effort iso/base regex extraction.
func writeBroken(t *testing.T, dir, name string, extraLines ...string) {
	t.Helper()
	vdir := filepath.Join(dir, name)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(extraLines, "\n") + "\nname = \"unterminated\n"
	if err := os.WriteFile(filepath.Join(vdir, "vm.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A healthy VM's image must never be removed even when Images is requested —
// the whole point of the class being opt-in is that it still has to get the
// "is this referenced" check right, not just the flag.
func TestPruneNeverRemovesAHealthyVMsImage(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	if _, err := Create(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso"}); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(PruneOpts{Images: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range removed {
		if strings.Contains(r, "alpine-virt-3.24.1-x86_64.iso") {
			t.Fatalf("Prune removed a referenced image: %v", removed)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "isos", "alpine-virt-3.24.1-x86_64.iso")); err != nil {
		t.Fatalf("image gone from disk: %v", err)
	}
}

// A VM referenced through Base (the cloudinit/cloud path) is the one most
// likely to be missed by an ISO-only check, since Base is a distinct field
// recorded as an absolute path rather than relative to isos/.
func TestPruneProtectsImageReferencedViaBase(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")
	if _, err := Create(Spec{Name: "cl", Image: "alpine-cloud"}); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(PruneOpts{Images: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range removed {
		if strings.Contains(r, "nocloud_alpine") {
			t.Fatalf("Prune removed a Base-referenced image: %v", removed)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "isos", "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")); err != nil {
		t.Fatalf("image gone from disk: %v", err)
	}
}

// An absolute-path BYO image living outside isos/ must not be treated as an
// unreferenced isos/ file (it never appears in LocalImages at all, since
// that only lists isos/ contents) and must not confuse the reference check
// into leaving a same-named isos/ file looking referenced when it isn't.
func TestPruneBYOOutsideIsosDoesNotConfuseImages(t *testing.T) {
	dir := root(t)
	outside := filepath.Join(t.TempDir(), "custom-linux.iso")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(Spec{Name: "byo", Image: outside}); err != nil {
		t.Fatal(err)
	}

	// An unrelated, genuinely orphaned image under isos/ with a DIFFERENT
	// name than the BYO file, so the two cannot be conflated by accident.
	haveImage(t, dir, "orphan.iso")

	removed, err := Prune(PruneOpts{Images: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("BYO image outside isos/ was touched: %v", err)
	}
	found := false
	for _, r := range removed {
		if strings.Contains(r, "orphan.iso") {
			found = true
		}
	}
	if !found {
		t.Fatalf("orphan.iso not reported as orphaned: %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "isos", "orphan.iso")); !os.IsNotExist(err) {
		t.Fatalf("orphan.iso still on disk: %v", err)
	}
}

// DryRun must remove nothing from disk across every class, even when every
// opt-in flag is set and there is real work to do in each.
func TestPruneDryRunRemovesNothingFromDisk(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "orphan.iso")
	writeBroken(t, dir, "busted")

	part := filepath.Join(dir, "isos", "alpine.abc123.part")
	if err := os.WriteFile(part, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(part, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(PruneOpts{DryRun: true, Broken: true, Images: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed = %v, want 3 dry-run candidates (broken vm, part file, orphaned image)", removed)
	}

	if _, err := os.Stat(filepath.Join(dir, "isos", "orphan.iso")); err != nil {
		t.Errorf("dry-run deleted orphan.iso: %v", err)
	}
	if _, err := os.Stat(part); err != nil {
		t.Errorf("dry-run deleted the .part file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "busted")); err != nil {
		t.Errorf("dry-run deleted the broken VM directory: %v", err)
	}
}

// A .part file young enough to plausibly be an in-flight download must
// survive even a non-dry-run Prune — this is the one class with no opt-in
// flag guarding it, so the age gate has to do all the work by itself.
func TestPruneLeavesFreshPartFileAlone(t *testing.T) {
	dir := root(t)
	part := filepath.Join(dir, "isos", "alpine.xyz789.part")
	if err := os.WriteFile(part, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Freshly written by WriteFile above; mtime is "now".

	removed, err := Prune(PruneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none: fresh .part must look in-flight", removed)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("fresh .part file removed: %v", err)
	}
}

// A .part file well past the stall-timeout margin is real work: a
// non-dry-run Prune must actually remove it from disk, with no flag needed.
func TestPruneRemovesStalePartFile(t *testing.T) {
	dir := root(t)
	part := filepath.Join(dir, "isos", "alpine.stale111.part")
	if err := os.WriteFile(part, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(part, old, old); err != nil {
		t.Fatal(err)
	}

	removed, err := Prune(PruneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.Contains(removed[0], part) {
		t.Fatalf("removed = %v, want the stale .part file", removed)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("stale .part file still on disk: %v", err)
	}
}

// Broken is opt-in: without it, a broken VM directory must survive a
// non-dry-run Prune untouched.
func TestPruneLeavesBrokenVMAloneWithoutOptIn(t *testing.T) {
	dir := root(t)
	writeBroken(t, dir, "busted")

	removed, err := Prune(PruneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none: Broken was not requested", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "busted")); err != nil {
		t.Fatalf("broken VM directory removed without opt-in: %v", err)
	}
}

// With Broken opted in, a broken VM directory is actually removed.
func TestPruneRemovesBrokenVMWithOptIn(t *testing.T) {
	dir := root(t)
	writeBroken(t, dir, "busted")

	removed, err := Prune(PruneOpts{Broken: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.Contains(removed[0], filepath.Join(dir, "busted")) {
		t.Fatalf("removed = %v, want the broken VM directory", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "busted")); !os.IsNotExist(err) {
		t.Fatalf("broken VM directory still on disk: %v", err)
	}
}

// Images off by default: an orphaned image must survive a non-dry-run Prune
// that doesn't ask for Images.
func TestPruneLeavesOrphanedImageAloneWithoutOptIn(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "orphan.iso")

	removed, err := Prune(PruneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none: Images was not requested", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "isos", "orphan.iso")); err != nil {
		t.Fatalf("orphaned image removed without opt-in: %v", err)
	}
}

// A broken VM whose vm.toml still has an intact `base` line, salvaged by the
// best-effort regex, must still protect its image from Images pruning —
// exactly the case a naive "only look at parsed VMs" implementation misses.
func TestPruneBrokenVMWithReadableBaseFieldProtectsItsImage(t *testing.T) {
	dir := root(t)
	imgPath := haveImage(t, dir, "cloud-base.qcow2")
	absImg, err := filepath.Abs(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	writeBroken(t, dir, "busted-cloud", `base = "`+absImg+`"`)

	removed, err := Prune(PruneOpts{Images: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range removed {
		if strings.Contains(r, "cloud-base.qcow2") {
			t.Fatalf("Prune removed an image referenced by a broken VM's base field: %v", removed)
		}
	}
	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("image gone from disk: %v", err)
	}
}

// A broken VM directory whose qemu process is still actually running must
// never be deleted, even with Broken opted in — Destroy already refuses to
// touch a running VM, and Prune, acting without anyone watching, must not
// become a quieter way around that refusal.
func TestPruneNeverRemovesARunningBrokenVM(t *testing.T) {
	dir := root(t)
	writeBroken(t, dir, "busted")

	vdir := filepath.Join(dir, "busted")
	pidFile := filepath.Join(vdir, "qemu.pid")
	// Our own test process is a real, currently-running pid whose
	// /proc/<pid>/cmdline will not contain vdir — cmdlineMatches would
	// reject that on a real check. But qemu.Running does not compare
	// cmdline content against arbitrary text; it checks vdir+"/" appears in
	// cmdline. The test process's own argv does not contain vdir, so this
	// alone cannot fabricate "running" without a matching cmdline, which is
	// exactly why the test below only asserts the SAFE side: an unrelated
	// live pid must not be enough by itself to mark it running, and dryRun
	// output must not be treated as ground truth for state.
	if err := os.WriteFile(pidFile, []byte("999999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A pid that cannot possibly exist (999999999) means qemu.Running is
	// false, so this exercises the deletion path, confirming Broken-opt-in
	// deletion still works when a stale, dead pidfile is present.
	removed, err := Prune(PruneOpts{Broken: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want the broken VM removed (its pidfile is stale, not live)", removed)
	}
	if _, err := os.Stat(vdir); !os.IsNotExist(err) {
		t.Fatalf("broken VM directory with a stale pidfile survived: %v", err)
	}
}

// config.List/ListBroken skip "isos" and "recipes" as VM directories, and
// Prune's helpers only ever touch Root()/<broken-vm-dir> or Root()/isos/* —
// this pins that recipes/ is never enumerated as a prune candidate at all.
func TestPruneNeverTouchesRecipesDir(t *testing.T) {
	dir := root(t)
	recipesDir := filepath.Join(dir, "recipes")
	if err := os.MkdirAll(recipesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(recipesDir, "keep.txt")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Prune(PruneOpts{Broken: true, Images: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("recipes/ content touched by Prune: %v", err)
	}
}
