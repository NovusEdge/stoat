package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
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
// "isos/" onto a browsed path yields ".../isos/home/u/x.iso" — a path that
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
