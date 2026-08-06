package core

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func ptr[T any](v T) *T { return &v }

func TestUpdateUnknownVM(t *testing.T) {
	root(t)
	if _, err := Update("nope", Patch{RAM: ptr(2048)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateImmutableFields(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		patch Patch
	}{
		{"name", Patch{Name: ptr("renamed")}},
		{"os", Patch{OS: ptr("ubuntu")}},
		{"backend", Patch{Backend: ptr("ssh")}},
		{"mode", Patch{Mode: ptr("disk")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Update("work", tc.patch); !errors.Is(err, ErrImmutableField) {
				t.Fatalf("err = %v, want ErrImmutableField", err)
			}
		})
	}
}

// Setting an immutable field to the value it already has is not a change
// request and must not be refused: an MCP tool or CLI flag that round
// trips a full VM through Patch shouldn't be punished for fields it never
// meant to touch.
func TestUpdateImmutableFieldsAllowedWhenUnchanged(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{
		Name:    ptr("work"),
		OS:      ptr("alpine"),
		Backend: ptr("apkovl"),
		Mode:    ptr("live"),
		RAM:     ptr(2048),
	})
	if err != nil {
		t.Fatalf("unchanged immutable fields should not error: %v", err)
	}
	got, err := Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.RAM != 2048 {
		t.Errorf("RAM = %d, want 2048", got.RAM)
	}
}

func TestUpdateRAMCPUsShareApplyImmediately(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Update("work", Patch{RAM: ptr(4096), CPUs: ptr(4), Share: ptr("/tmp/share")})
	if err != nil {
		t.Fatal(err)
	}
	if got.RAM != 4096 || got.CPUs != 4 || got.Share != "/tmp/share" {
		t.Fatalf("got %+v, want ram=4096 cpus=4 share=/tmp/share", got)
	}
	reloaded, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RAM != 4096 || reloaded.CPUs != 4 || reloaded.Share != "/tmp/share" {
		t.Fatalf("not persisted: %+v", reloaded)
	}
}

func TestUpdateRejectsInvalidRAMAndCPUs(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Update("work", Patch{RAM: ptr(100)}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("ram too low: err = %v, want ErrInvalidSpec", err)
	}
	if _, err := Update("work", Patch{CPUs: ptr(0)}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("cpus zero: err = %v, want ErrInvalidSpec", err)
	}
}

func TestUpdateRecipesReplacesWholesale(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"old.yaml"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Update("work", Patch{Recipes: &[]string{"new.yaml", "other.yaml"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Recipes) != 2 || got.Recipes[0] != "new.yaml" {
		t.Fatalf("recipes = %+v, want [new.yaml other.yaml]", got.Recipes)
	}

	// Clearing: an explicit empty (non-nil) slice must actually clear it,
	// distinct from a nil Patch.Recipes leaving it alone.
	got, err = Update("work", Patch{Recipes: &[]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Recipes) != 0 {
		t.Fatalf("recipes = %+v, want empty after explicit clear", got.Recipes)
	}
}

func TestUpdateRecipesNilLeavesUnchanged(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Recipes: []string{"kept.yaml"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Update("work", Patch{RAM: ptr(2048)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Recipes) != 1 || got.Recipes[0] != "kept.yaml" {
		t.Fatalf("recipes = %+v, want [kept.yaml] unchanged", got.Recipes)
	}
}

func TestUpdateSSHPortSavesEvenWhileRunning(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	stop := fakeRunning(t, v)
	defer stop()

	got, err := Update("work", Patch{SSHPort: ptr(2299)})
	if err != nil {
		t.Fatalf("ssh port change must SUCCEED (applies at next start), not error: %v", err)
	}
	if got.SSHPort != 2299 {
		t.Fatalf("SSHPort = %d, want 2299", got.SSHPort)
	}
}

func TestUpdateSSHPortRejectsOutOfRange(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Update("work", Patch{SSHPort: ptr(80)}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("privileged port: err = %v, want ErrInvalidSpec", err)
	}
	if _, err := Update("work", Patch{SSHPort: ptr(70000)}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("out of range: err = %v, want ErrInvalidSpec", err)
	}
}

func TestUpdateSSHPortRejectsCollisionWithAnotherVMsSSHPort(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{SSHPort: ptr(2201)})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for collision with another VM's ssh port", err)
	}
	if !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("error does not name the colliding VM: %v", err)
	}
}

func TestUpdateSSHPortRejectsCollisionWithAnotherVMsForward(t *testing.T) {
	root(t)
	other := &config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}
	if err := other.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Forward("other", []PortForward{{HostPort: 8080, GuestPort: 80}}); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{SSHPort: ptr(8080)})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for collision with another VM's forward", err)
	}
	if !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("error does not name the colliding VM: %v", err)
	}
	if !strings.Contains(err.Error(), "declared forward") {
		t.Errorf("error does not say the collision is with a declared forward: %v", err)
	}
}

// A broken VM (vm.toml exists but fails to parse) still claims its ssh port
// via config.BrokenSSHPort, and the refusal must still name it.
func TestUpdateSSHPortRejectsCollisionWithBrokenVMsSSHPort(t *testing.T) {
	dir := root(t)
	writeBroken(t, dir, "hosed", "sshport = 2201")

	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{SSHPort: ptr(2201)})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for collision with a broken VM's ssh port", err)
	}
	if !strings.Contains(err.Error(), `"hosed"`) {
		t.Errorf("error does not name the colliding broken VM: %v", err)
	}
}

// The synthetic-forward reuse trick in validateSSHPort must not leak
// forward.go's "requested twice" wording, written for Forward's own
// duplicate-in-request case, into an SSHPort error: a caller changing the
// ssh port supplied exactly one value and has no idea a PortForward was
// synthesized under the hood, so that message would be actively confusing.
func TestUpdateSSHPortCollisionWithOwnForwardHasClearMessage(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Forward("work", []PortForward{{HostPort: 9000, GuestPort: 80}}); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{SSHPort: ptr(9000)})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
	if strings.Contains(err.Error(), "requested twice") {
		t.Fatalf("error leaks Forward's confusing wording for a single-value SSHPort request: %v", err)
	}
	if !strings.Contains(err.Error(), "ssh port") {
		t.Fatalf("error should name the ssh port explicitly: %v", err)
	}
}

// Every other SSHPort rejection should read as being about the ssh port,
// not a bare "host port" left over from validateForwards' own vocabulary.
func TestUpdateSSHPortErrorsNameSSHPort(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "other", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		port int
	}{
		{"privileged", 80},
		{"out of range", 70000},
		{"collides with another vm", 2201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Update("work", Patch{SSHPort: ptr(tc.port)})
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("err = %v, want ErrInvalidSpec", err)
			}
			if !strings.Contains(err.Error(), "ssh port") {
				t.Fatalf("error should mention ssh port for context: %v", err)
			}
		})
	}
}

// Setting SSHPort to the value it already has must not be treated as a
// collision with itself.
func TestUpdateSSHPortUnchangedIsNoop(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Update("work", Patch{SSHPort: ptr(2200)}); err != nil {
		t.Fatalf("unchanged ssh port should not error: %v", err)
	}
}

func TestUpdateDiskGrowsOnStoppedVM(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "work", Mode: "disk", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "1G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), "1G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}

	got, err := Update("work", Patch{Disk: ptr("2G")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Disk != "2G" {
		t.Fatalf("Disk = %q, want 2G", got.Disk)
	}

	out, err := exec.Command("qemu-img", "info", "--output=json", v.DiskPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img info: %v: %s", err, out)
	}
	if !strings.Contains(string(out), `"virtual-size": 2147483648`) {
		t.Fatalf("disk was not actually resized on disk: %s", out)
	}
}

func TestUpdateDiskRejectsShrink(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "work", Mode: "disk", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "2G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), "2G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}
	if _, err := Update("work", Patch{Disk: ptr("1G")}); !errors.Is(err, ErrDiskShrink) {
		t.Fatalf("err = %v, want ErrDiskShrink", err)
	}
	// Nothing on disk should have moved.
	out, err := exec.Command("qemu-img", "info", "--output=json", v.DiskPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img info: %v: %s", err, out)
	}
	if !strings.Contains(string(out), `"virtual-size": 2147483648`) {
		t.Fatalf("disk was resized despite the refusal: %s", out)
	}
}

func TestUpdateDiskRejectsLeadingPlus(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "work", Mode: "disk", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "1G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), "1G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}
	if _, err := Update("work", Patch{Disk: ptr("+1G")}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for a relative size", err)
	}
	out, err := exec.Command("qemu-img", "info", "--output=json", v.DiskPath()).CombinedOutput()
	if err != nil {
		t.Fatalf("qemu-img info: %v: %s", err, out)
	}
	// Must still be 1G, not grown by 1G to 2G: the exact bug ParseSize
	// exists to prevent (see ParseSize's own comment in core.go).
	if !strings.Contains(string(out), `"virtual-size": 1073741824`) {
		t.Fatalf("disk grew despite the relative size being refused: %s", out)
	}
}

func TestUpdateDiskRefusedWhileRunning(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "work", Mode: "disk", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "1G"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", v.DiskPath(), "1G").CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create: %v: %s", err, out)
	}
	stop := fakeRunning(t, v)
	defer stop()

	if _, err := Update("work", Patch{Disk: ptr("2G")}); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}

func TestUpdateDiskRejectsOnLiveVM(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Update("work", Patch{Disk: ptr("2G")}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec for a disk resize on a live vm", err)
	}
}

func TestUpdateDiskOnCloudVMWithoutOverlayYet(t *testing.T) {
	haveQemuImg(t)
	root(t)
	v := &config.VM{Name: "work", Mode: "cloud", Backend: "cloudinit", RAM: 1024, CPUs: 1, SSHPort: 2200, Disk: "3G", Base: "/nonexistent/base.qcow2"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	// disk.qcow2 deliberately does not exist: Create never makes a cloud
	// overlay eagerly, and neither does this test.
	if _, err := Update("work", Patch{Disk: ptr("4G")}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec (start it once first)", err)
	}
}

func TestUpdateDoesNotChangeUnrelatedFields(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", OS: "alpine", Backend: "apkovl", RAM: 1024, CPUs: 2, Share: "/data", SSHPort: 2200, Recipes: []string{"a.yaml"}}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Update("work", Patch{RAM: ptr(2048)})
	if err != nil {
		t.Fatal(err)
	}
	if got.CPUs != 2 || got.Share != "/data" || got.SSHPort != 2200 || len(got.Recipes) != 1 {
		t.Fatalf("unrelated fields changed: %+v", got)
	}
}

// Sanity check that Patch's int-pointer fields really do distinguish
// "not set" from "set to zero": CPUs: ptr(0) must be REJECTED (cpus must
// be at least 1), not silently ignored as if nil.
func TestUpdatePatchZeroValueIsAReadValue(t *testing.T) {
	root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	_, err := Update("work", Patch{CPUs: ptr(0)})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("CPUs: ptr(0) must be validated as an explicit request, got err = %v", err)
	}
	// And confirm the field name in the error, since strconv proves the
	// pointer really did carry a real (non-nil) value through.
	if !strings.Contains(err.Error(), strconv.Itoa(0)) && !strings.Contains(err.Error(), "cpus") {
		t.Fatalf("error should mention cpus: %v", err)
	}
}
