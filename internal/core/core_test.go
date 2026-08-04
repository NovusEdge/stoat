package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/recipes"
)

// root points the data root at a temp dir and returns it. Every test here
// resolves images and allocates ports against the real config.Root(), so
// nothing may touch the user's ~/.stoat.
func root(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STOAT_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "isos"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// haveImage drops a file into isos/ so a catalog entry counts as downloaded.
func haveImage(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, "isos", name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanDefaults(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	v, err := plan(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso"})
	if err != nil {
		t.Fatal(err)
	}
	if v.RAM != DefaultRAM || v.CPUs != DefaultCPUs {
		t.Errorf("RAM/CPUs = %d/%d, want %d/%d", v.RAM, v.CPUs, DefaultRAM, DefaultCPUs)
	}
	if v.Mode != "live" || v.Backend != "apkovl" || v.OS != "alpine" {
		t.Errorf("got %s/%s/%s, want live/apkovl/alpine", v.Mode, v.Backend, v.OS)
	}
	// Live mode boots off the ISO and allocates no qcow2, so it must NOT get
	// the disk-mode default handed to qemu-img.
	if v.Disk != "" {
		t.Errorf("Disk = %q on a live VM, want empty", v.Disk)
	}
	if v.ISO != "isos/alpine-virt-3.24.1-x86_64.iso" {
		t.Errorf("ISO = %q", v.ISO)
	}
	if v.SSHPort == 0 {
		t.Error("no ssh port allocated")
	}
}

func TestPlanDiskModeGetsADefaultSize(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	v, err := plan(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Mode: "disk"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Disk != DefaultDisk {
		t.Errorf("Disk = %q, want %q", v.Disk, DefaultDisk)
	}
}

// A cloud image is dispatched by the backend its ENTRY declares, not by its
// OS: alpine-cloud is OS alpine but backend cloudinit. Recording apkovl here
// would hand it an apkovl drive it never boots from and no cloud-init seed at
// all, and it would connect as root, which cloud images lock.
func TestPlanAlpineCloudIsCloudinitNotApkovl(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")

	v, err := plan(Spec{Name: "cl", Image: "alpine-cloud"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Backend != "cloudinit" || v.Mode != "cloud" || v.OS != "alpine" {
		t.Fatalf("got %s/%s/%s, want cloudinit/cloud/alpine", v.Backend, v.Mode, v.OS)
	}
	if v.SSHUser != cloudinit.User {
		t.Errorf("SSHUser = %q, want %q", v.SSHUser, cloudinit.User)
	}
	if v.Base == "" || v.ISO != "" {
		t.Errorf("Base = %q, ISO = %q; a cloud VM boots an overlay off Base, not an ISO", v.Base, v.ISO)
	}
	if v.ConsolePassword == "" {
		t.Error("no console password; a cloud VM's console would have no valid login")
	}
}

// filepath.Join does not special-case an absolute second element, so joining
// "isos/" onto a browsed path yields ".../isos/home/u/x.iso", a path that
// does not exist. An image outside isos/ has to be recorded absolute.
func TestPlanBYOOutsideIsosRecordsAnAbsolutePath(t *testing.T) {
	root(t)
	outside := filepath.Join(t.TempDir(), "custom-linux.iso")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := plan(Spec{Name: "byo", Image: outside})
	if err != nil {
		t.Fatal(err)
	}
	if v.ISO != outside {
		t.Errorf("ISO = %q, want %q", v.ISO, outside)
	}
	if v.ISOPath() != outside {
		t.Errorf("ISOPath() = %q, want %q", v.ISOPath(), outside)
	}
}

func TestPlanBYOOverridesApplyOnlyToBYO(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "custom.qcow2")
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")

	v, err := plan(Spec{Name: "byo", Image: "custom.qcow2", OS: "debian"})
	if err != nil {
		t.Fatal(err)
	}
	if v.OS != "debian" {
		t.Errorf("OS = %q, want the override debian", v.OS)
	}

	// A catalog entry states its own OS and is authoritative; an override
	// would let a caller mislabel a known image and get the wrong recipes and
	// the wrong guest shell.
	v, err = plan(Spec{Name: "cat", Image: "alpine-cloud", OS: "debian"})
	if err != nil {
		t.Fatal(err)
	}
	if v.OS != "alpine" {
		t.Errorf("OS = %q, want alpine: a catalog entry's OS is not overridable", v.OS)
	}
}

func TestPlanRefusesRelativeDiskSize(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	_, err := plan(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Mode: "disk", Disk: "+8G"})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec: +8G means GROW BY to qemu-img", err)
	}
}

func TestPlanRefusesDuplicateName(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	if err := os.MkdirAll(filepath.Join(dir, "work"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := plan(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso"})
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

func TestPlanUndownloadedCatalogEntry(t *testing.T) {
	root(t)
	_, err := plan(Spec{Name: "work", Image: "alpine-cloud"})
	if !errors.Is(err, ErrImageNotDownloaded) {
		t.Fatalf("err = %v, want ErrImageNotDownloaded", err)
	}
}

func TestPlanUnknownImage(t *testing.T) {
	root(t)
	_, err := plan(Spec{Name: "work", Image: "no-such-thing"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPlanRejectsBadNames(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	for _, name := range []string{"", "  ", "a/b", "a b"} {
		if _, err := plan(Spec{Name: name, Image: "alpine-virt-3.24.1-x86_64.iso"}); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("plan(name=%q) err = %v, want ErrInvalidSpec", name, err)
		}
	}
}

func TestModeFor(t *testing.T) {
	cases := []struct {
		backend, want, mode string
		bad                 bool
	}{
		{backend: "apkovl", want: "", mode: "live"},
		{backend: "apkovl", want: "live", mode: "live"},
		{backend: "apkovl", want: "disk", mode: "disk"},
		{backend: "apkovl", want: "cloud", bad: true},
		{backend: "cloudinit", want: "", mode: "cloud"},
		{backend: "cloudinit", want: "live", bad: true},
		{backend: "ssh", want: "", mode: "disk"},
		{backend: "ssh", want: "live", bad: true},
	}
	for _, c := range cases {
		got, err := modeFor(c.backend, c.want)
		if c.bad {
			if err == nil {
				t.Errorf("modeFor(%q, %q) = %q, want an error", c.backend, c.want, got)
			}
			continue
		}
		if err != nil || got != c.mode {
			t.Errorf("modeFor(%q, %q) = %q, %v; want %q", c.backend, c.want, got, err, c.mode)
		}
	}
}

// A failed qemu-img must leave no trace: v.Save() runs before it, so without
// the cleanup a bad create leaves a phantom VM in the list with no disk.qcow2,
// which can never boot. 9999999T parses as a size and is one qemu-img refuses.
func TestCreateFailedDiskCreationLeavesNoTrace(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	_, err := Create(Spec{Name: "badsize", Image: "alpine-virt-3.24.1-x86_64.iso", Mode: "disk", Disk: "9999999T"})
	if err == nil {
		t.Fatal("expected qemu-img to refuse the size")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "badsize")); !os.IsNotExist(statErr) {
		t.Fatalf("VM directory survived a failed disk creation: %v", statErr)
	}
}

func TestCreateWritesVMToml(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}
	// A real recipe name, as recipes.List returns it; plan now refuses names
	// that are not actually available for the VM's OS and backend.
	v, err := Create(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Recipes: []string{"devtools.alpine.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHPort != v.SSHPort || got.OS != "alpine" || strings.Join(got.Recipes, ",") != "devtools.alpine.sh" {
		t.Errorf("reloaded %+v, want it to match %+v", got, v)
	}
}

// TestConcurrentCreatesGetDistinctPorts is the regression test for the §11
// concurrency gap: FreePort reads every VM's port, picks a free one and
// returns it, but the caller only COMMITS that choice when it writes vm.toml
// some time later. Two callers interleaved in that gap both saw the same free
// port and both took it, producing two VMs that fight over one host socket,
// which surfaces much later as a bind failure from qemu naming neither VM.
//
// The CLI could not hit this (one VM per invocation), but an MCP server
// handling two tool calls, or simply two `stoat create` processes, can.
func TestConcurrentCreatesGetDistinctPorts(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	const n = 8
	var wg sync.WaitGroup
	ports := make([]int, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them all at once, to actually overlap
			v, err := Create(Spec{
				Name:  fmt.Sprintf("vm%d", i),
				Image: "alpine-virt-3.24.1-x86_64.iso",
			})
			if err != nil {
				errs[i] = err
				return
			}
			ports[i] = v.SSHPort
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[int]string{}
	for i, p := range ports {
		if errs[i] != nil {
			t.Fatalf("vm%d: %v", i, errs[i])
		}
		if prev, dup := seen[p]; dup {
			t.Fatalf("vm%d and %s were both given ssh port %d", i, prev, p)
		}
		seen[p] = fmt.Sprintf("vm%d", i)
	}

	// And the ports on DISK must match: a port held only in the returned
	// struct would still collide for anything that reads vm.toml later.
	onDisk := map[int]string{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("vm%d", i)
		v, err := config.Load(name)
		if err != nil {
			t.Fatal(err)
		}
		if prev, dup := onDisk[v.SSHPort]; dup {
			t.Fatalf("%s and %s both persisted ssh port %d", name, prev, v.SSHPort)
		}
		onDisk[v.SSHPort] = name
	}
}

// TestCloudVMGetsADiskSize pins that a cloud VM created through core carries a
// disk size, matching what the TUI form has always produced.
//
// A cloud VM's qcow2 is a CoW overlay and inherits its BASE image's virtual
// size: Ubuntu 24.04's is 3.5G with a 2.4G root. backend/cloudinit.go only
// resizes when Disk is set, so an empty size means the overlay is never grown,
// installing a desktop fills it, apt exits 100, and cloud-init reports a bare
// "error" that reads as a broken recipe. That was diagnosed and fixed once
// before; defaulting only disk mode reintroduced it on the CLI path while the
// TUI (whose form pre-fills 8G for every mode) stayed correct.
func TestCloudVMGetsADiskSize(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")

	v, err := plan(Spec{Name: "cl", Image: "alpine-cloud"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Mode != "cloud" {
		t.Fatalf("mode = %q, want cloud", v.Mode)
	}
	if v.Disk == "" {
		t.Fatal("a cloud VM was planned with no disk size: its overlay would inherit the base image's, which cannot fit a desktop")
	}

	// An explicit size still wins over the default.
	v, err = plan(Spec{Name: "cl2", Image: "alpine-cloud", Disk: "20G"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Disk != "20G" {
		t.Errorf("Disk = %q, want the explicit 20G", v.Disk)
	}
}

// TestCreateRejectsAnUnavailableRecipe pins that a recipe this VM cannot run
// is refused at CREATE time.
//
// recipes.List returns full filenames ("xfce.cloud.yaml") and recipes.Read
// expects the same, because the suffix separates ssh-pushed shell recipes from
// cloud-init fragments. Nothing checked a Spec's names against that list, so
// `--recipes xfce` was accepted, written to vm.toml, and failed only on
// `stoat up` with "open .../recipes/xfce: no such file or directory", a
// create that succeeded and produced a VM that could not start. Hit for real
// while boot-testing.
func TestCreateRejectsAnUnavailableRecipe(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")
	// The recipes stoat ships, written into the temp root exactly as every
	// TUI and CLI start does.
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	_, err := plan(Spec{Name: "cl", Image: "alpine-cloud", Recipes: []string{"xfce"}})
	if !errors.Is(err, ErrRecipeNotApplicable) {
		t.Fatalf("err = %v, want ErrRecipeNotApplicable for a bare name", err)
	}
	// The message must name what IS available: the failure is nearly always a
	// close-but-wrong name, and a caller who cannot list a directory (an
	// agent) otherwise has nothing to correct against.
	if !strings.Contains(err.Error(), ".cloud.yaml") {
		t.Errorf("error does not say what is available: %v", err)
	}

	// The full filename, as List returns it, is accepted.
	if _, err := plan(Spec{Name: "cl", Image: "alpine-cloud", Recipes: []string{"xfce.alpine.cloud.yaml"}}); err != nil {
		t.Fatalf("a recipe straight from recipes.List was rejected: %v", err)
	}
	// And no recipes at all stays valid.
	if _, err := plan(Spec{Name: "cl", Image: "alpine-cloud"}); err != nil {
		t.Fatalf("no recipes should be fine: %v", err)
	}
}
