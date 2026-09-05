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

// A cloud image is dispatched by the backend its entry declares, not by its
// OS. alpine-cloud is OS alpine but backend cloudinit. Recording apkovl here
// would give it an apkovl drive it never boots from, no cloud-init seed, and
// a root login that cloud images lock.
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

// filepath.Join does not special-case an absolute second element. Joining
// "isos/" onto a browsed path yields ".../isos/home/u/x.iso", a path that
// does not exist. An image outside isos/ must be recorded as an absolute
// path.
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

func TestPlanRejectsInvalidDisplay(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	_, err := plan(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Display: "fullscreen"})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func TestPlanAcceptsEveryValidDisplay(t *testing.T) {
	dir := root(t)
	for _, pref := range []string{"", "auto", "window", "vnc"} {
		haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
		v, err := plan(Spec{Name: "work-" + pref, Image: "alpine-virt-3.24.1-x86_64.iso", Display: pref})
		if err != nil {
			t.Fatalf("Display=%q: %v", pref, err)
		}
		if v.Display != pref {
			t.Errorf("Display = %q, want %q", v.Display, pref)
		}
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

// A failed qemu-img must leave no trace. v.Save() runs before it, so
// without cleanup a bad create leaves a phantom VM in the list with no
// disk.qcow2, one that can never boot. 9999999T parses as a size but is one
// qemu-img refuses.
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
	v, err := Create(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso", Recipes: []string{"devtools"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHPort != v.SSHPort || got.OS != "alpine" || strings.Join(got.Recipes, ",") != "devtools" {
		t.Errorf("reloaded %+v, want it to match %+v", got, v)
	}
}

// TestCreateAllowExecFollowsTheDefaultLevel pins that a Spec which names
// neither AllowExec nor AgentAccess, such as the TUI's form or a stoat.toml
// declaration without agent_access, lands on the default level manage, and
// that allow_exec on disk agrees with it. Before agent_access existed the
// default was allow_exec = true; the level is the source of truth now.
func TestCreateAllowExecFollowsTheDefaultLevel(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	v, err := Create(Spec{Name: "work", Image: "alpine-virt-3.24.1-x86_64.iso"})
	if err != nil {
		t.Fatal(err)
	}
	if v.AgentAccess != "manage" || v.AllowExec {
		t.Errorf("Create with nothing said: agent_access = %q allow_exec = %v, want manage and false", v.AgentAccess, v.AllowExec)
	}
	got, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentAccess != "manage" || got.AllowExec {
		t.Errorf("reloaded vm.toml: agent_access = %q allow_exec = %v, want manage and false", got.AgentAccess, got.AllowExec)
	}
}

// TestCreateLegacyAllowExecTrueMeansExec pins the one caller that still
// speaks AllowExec alone: a true pointer with no level lands on exec, the
// same mapping config.Load applies to an old vm.toml.
func TestCreateLegacyAllowExecTrueMeansExec(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	yes := true
	v, err := Create(Spec{Name: "legacy", Image: "alpine-virt-3.24.1-x86_64.iso", AllowExec: &yes})
	if err != nil {
		t.Fatal(err)
	}
	if v.AgentAccess != "exec" || !v.AllowExec {
		t.Errorf("Create with AllowExec &true: agent_access = %q allow_exec = %v, want exec and true", v.AgentAccess, v.AllowExec)
	}
}

// TestCreateAllowExecFalseIsHonoured is TestCreateAllowExecDefaultsTrue's
// mirror: an explicit opt-out must survive Create and the roundtrip through
// vm.toml.
func TestCreateAllowExecFalseIsHonoured(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")

	no := false
	v, err := Create(Spec{Name: "locked-down", Image: "alpine-virt-3.24.1-x86_64.iso", AllowExec: &no})
	if err != nil {
		t.Fatal(err)
	}
	if v.AllowExec {
		t.Errorf("Create with AllowExec: &false should produce AllowExec false, got true")
	}
	got, err := config.Load("locked-down")
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowExec {
		t.Errorf("reloaded vm.toml should have allow_exec = false, got true")
	}
}

// TestConcurrentCreatesGetDistinctPorts is the regression test for the §11
// concurrency gap. FreePort reads every VM's port and picks a free one, but
// the caller only commits that choice when it writes vm.toml later. Two
// callers interleaved in that gap could both pick the same port, producing
// two VMs that fight over one host socket. The failure then surfaces much
// later, as a bind failure from qemu naming neither VM.
//
// The CLI could not hit this: it creates one VM per invocation. An MCP
// server handling two tool calls, or two `stoat create` processes, can.
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

// TestCloudVMGetsADiskSize pins that a cloud VM created through core carries
// a disk size, matching what the TUI form has always produced.
//
// A cloud VM's qcow2 is a CoW overlay and inherits its base image's virtual
// size. Ubuntu 24.04's is 3.5G with a 2.4G root. backend/cloudinit.go
// resizes the overlay only when Disk is set. With no size, installing a
// desktop fills the overlay, apt exits 100, and cloud-init reports a bare
// "error" that reads as a broken recipe. Defaulting only disk mode
// reintroduced this bug on the CLI path; the TUI's form, which pre-fills 8G
// for every mode, stayed correct.
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
// recipes.List returns v2 recipe names ("xfce"). Before this check, nothing
// verified a Spec's names against that list, so `--recipes
// nonexistent-recipe` was accepted, written to vm.toml, and failed only on
// `stoat up`, a create that succeeded but produced a VM that could not
// start.
func TestCreateRejectsAnUnavailableRecipe(t *testing.T) {
	dir := root(t)
	haveImage(t, dir, "nocloud_alpine-3.24.1-x86_64-bios-tiny-r0.qcow2")
	// The recipes stoat ships, written into the temp root exactly as every
	// TUI and CLI start does.
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	_, err := plan(Spec{Name: "cl", Image: "alpine-cloud", Recipes: []string{"nonexistent-recipe"}})
	if !errors.Is(err, ErrRecipeNotApplicable) {
		t.Fatalf("err = %v, want ErrRecipeNotApplicable for an unknown name", err)
	}
	// The message must name what is available. The failure is nearly always
	// a close-but-wrong name, and a caller who cannot list a directory, an
	// agent, has nothing else to correct against.
	if !strings.Contains(err.Error(), "xfce") {
		t.Errorf("error does not say what is available: %v", err)
	}

	// A real recipe name, as List returns it, is accepted.
	if _, err := plan(Spec{Name: "cl", Image: "alpine-cloud", Recipes: []string{"xfce"}}); err != nil {
		t.Fatalf("a recipe straight from recipes.List was rejected: %v", err)
	}
	// And no recipes at all stays valid.
	if _, err := plan(Spec{Name: "cl", Image: "alpine-cloud"}); err != nil {
		t.Fatalf("no recipes should be fine: %v", err)
	}
}
