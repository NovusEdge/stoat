package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func haveQemuImg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}
}

func TestCloneUnknownSource(t *testing.T) {
	root(t)
	if _, err := Clone("nope", "clone1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCloneNameCollision(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := (&config.VM{Name: "taken", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Clone("work", "taken"); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

func TestCloneRefusesInvalidName(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "  ", "has space", "has/slash"} {
		if _, err := Clone("work", bad); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Clone(work, %q) err = %v, want ErrInvalidSpec", bad, err)
		}
	}
}

// A running source must be refused outright: an overlay's backing file must
// never change after the overlay is made, and a running qemu process is
// writing to its disk continuously. fakeRunning (vm_test.go) fakes liveness
// without a real qemu process.
func TestCloneRefusesRunningSource(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = filepath.Join(dir, "work")
	stop := fakeRunning(t, v)
	defer stop()

	if _, err := Clone("work", "clone1"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}

// A live VM has no disk.qcow2 at all — Clone must succeed without
// qemu-img and without fabricating one.
func TestCloneLiveModeCopiesConfigWithNoDisk(t *testing.T) {
	root(t)
	src := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl",
		RAM: 2048, CPUs: 2, SSHPort: 2200, Share: "/tmp/share",
		Recipes: []string{"a.sh"},
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}

	clone, err := Clone("work", "work2")
	if err != nil {
		t.Fatal(err)
	}
	if clone.Name != "work2" {
		t.Errorf("Name = %q, want work2", clone.Name)
	}
	if clone.OS != "alpine" || clone.Share != "/tmp/share" || len(clone.Recipes) != 1 {
		t.Errorf("config not copied: %+v", clone)
	}
	if clone.SSHPort == src.SSHPort {
		t.Errorf("clone SSHPort %d == source SSHPort, want a freshly allocated port", clone.SSHPort)
	}
	if _, err := os.Stat(filepath.Join(config.Root(), "work2", "disk.qcow2")); !os.IsNotExist(err) {
		t.Errorf("live mode clone should have no disk.qcow2, stat err = %v", err)
	}

	// The source itself must be untouched.
	again, err := Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if again.SSHPort != src.SSHPort {
		t.Errorf("source SSHPort changed: %d -> %d", src.SSHPort, again.SSHPort)
	}
}

// Port forwards are user-declared host listeners; inheriting them means the
// clone fights the source over the same host port the moment both run. They
// must not survive a clone.
func TestCloneDropsForwards(t *testing.T) {
	root(t)
	src := &config.VM{
		Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200,
		Forwards: []config.PortForward{{HostPort: 8080, GuestPort: 80}},
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}

	clone, err := Clone("work", "work2")
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.Forwards) != 0 {
		t.Errorf("Forwards = %+v, want dropped (empty)", clone.Forwards)
	}
}

// A disk-mode VM's disk.qcow2 is a real, independent file. The clone must
// become a qcow2 overlay backed by it — near-instant, and tiny until the
// clone diverges.
func TestCloneDiskModeCreatesOverlay(t *testing.T) {
	haveQemuImg(t)
	dir := root(t)
	src := &config.VM{Name: "work", Mode: "disk", OS: "alpine", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "1G", Installed: true}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}
	// Create matches what core.Create does for disk mode: a real qcow2, not
	// an overlay.
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", src.DiskPath(), "1G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}

	clone, err := Clone("work", "work2")
	if err != nil {
		t.Fatal(err)
	}
	if !clone.Installed {
		t.Error("Installed should be copied from the source")
	}
	clonePath := filepath.Join(dir, "work2", "disk.qcow2")
	if _, err := os.Stat(clonePath); err != nil {
		t.Fatalf("clone disk.qcow2 not created: %v", err)
	}
	out, err := exec.Command("qemu-img", "info", clonePath).CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img info: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "backing file:") {
		t.Errorf("clone disk is not a backing-file overlay:\n%s", out)
	}
	if !strings.Contains(string(out), filepath.Join(dir, "work", "disk.qcow2")) {
		t.Errorf("clone disk is not backed by the SOURCE's disk:\n%s", out)
	}

	// The source's own disk file must be untouched (still a plain qcow2,
	// still openable, still there).
	if _, err := os.Stat(src.DiskPath()); err != nil {
		t.Fatalf("source disk gone after clone: %v", err)
	}
}

// A cloud VM that has never started has no disk.qcow2 (see Create's own
// comment on why: the overlay is built lazily by cloudinitBackend.Prepare
// on first start). Clone must not fabricate one — the clone's own first
// start builds a fresh overlay-on-Base and a fresh seed, which is what gives
// it a genuinely new cloud-init instance ID.
func TestCloneCloudModeNeverStartedLeavesDiskToFirstStart(t *testing.T) {
	dir := root(t)
	base := filepath.Join(dir, "base.qcow2")
	if err := os.WriteFile(base, []byte("fake base"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &config.VM{
		Name: "web1", Mode: "cloud", Backend: "cloudinit", OS: "ubuntu",
		RAM: 2048, CPUs: 2, SSHPort: 2200, Base: base, SSHUser: "stoat",
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}

	clone, err := Clone("web1", "web2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "web2", "disk.qcow2")); !os.IsNotExist(err) {
		t.Errorf("never-started cloud clone should have no disk.qcow2 yet, stat err = %v", err)
	}
	clonedCfg, err := config.Load("web2")
	if err != nil {
		t.Fatal(err)
	}
	if clonedCfg.Base != base {
		t.Errorf("Base = %q, want %q (same catalog image)", clonedCfg.Base, base)
	}
	_ = clone
}

// A cloud VM that HAS started has real drift on its disk.qcow2. Cloning it
// must preserve that drift (overlay on the source's disk, not Base) AND
// must eagerly rebuild the seed against the clone's own name — because
// cloudinitBackend.Prepare no-ops the instant disk.qcow2 already exists, and
// would otherwise leave the clone with no seed.iso at all (a hard qemu
// start failure), not merely a stale instance ID.
func TestCloneCloudModeAlreadyStartedRebuildsSeedWithNewIdentity(t *testing.T) {
	if _, err := exec.LookPath("xorriso"); err != nil {
		t.Skip("xorriso not installed: seed rebuild needs it")
	}
	haveQemuImg(t)
	dir := root(t)
	base := filepath.Join(dir, "base.qcow2")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", base, "1G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create base: %v: %s", err, out)
	}
	src := &config.VM{
		Name: "web1", Mode: "cloud", Backend: "cloudinit", OS: "ubuntu",
		RAM: 2048, CPUs: 2, SSHPort: 2200, Base: base, SSHUser: "stoat",
	}
	if err := src.Save(); err != nil {
		t.Fatal(err)
	}
	// Simulate a booted source: an overlay-on-base disk, as
	// cloudinitBackend.Prepare would have produced.
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", base, "-F", "qcow2", src.DiskPath()).CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create overlay: %v: %s", err, out)
	}

	clone, err := Clone("web1", "web2")
	if err != nil {
		t.Fatal(err)
	}
	clonedCfg, err := config.Load("web2")
	if err != nil {
		t.Fatal(err)
	}

	clonePath := clonedCfg.DiskPath()
	out, err := exec.Command("qemu-img", "info", clonePath).CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img info: %v: %s", err, out)
	}
	if !strings.Contains(string(out), src.DiskPath()) {
		t.Errorf("booted-cloud clone disk should be backed by the SOURCE's disk, not Base:\n%s", out)
	}

	seedPath := filepath.Join(clonedCfg.OvlDir(), "seed", "meta-data")
	md, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("clone seed meta-data not written: %v", err)
	}
	if !strings.Contains(string(md), `"stoat-web2"`) {
		t.Errorf("clone instance-id does not name the CLONE: %q", string(md))
	}
	if strings.Contains(string(md), `"stoat-web1"`) {
		t.Errorf("clone instance-id still names the SOURCE: %q", string(md))
	}
	_ = clone
}
