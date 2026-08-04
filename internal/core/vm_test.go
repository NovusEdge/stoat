package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/testutil"
)

// writeRawVMToml writes vm.toml content directly, bypassing config.VM.Save
// and its TOML encoding, so a deliberately malformed file can be produced.
// Mirrors config_test.go's helper of the same name, reimplemented here
// rather than imported: it lives in package config_test, not exported for
// reuse.
func writeRawVMToml(t *testing.T, name, content string) {
	t.Helper()
	dir := filepath.Join(config.Root(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeRunning marks v as running without a real qemu process: it spawns
// `sleep`, whose argv includes v.Dir (qemu.Running's cmdlineMatches only
// checks that /proc/<pid>/cmdline contains dir+"/", not which binary it is),
// and points the VM's pidfile at it. The returned func kills the process and
// must be deferred by the caller.
//
// This exists so Start/Stop/Destroy's "is it running" branches are testable
// without qemu-system-x86_64 installed, exactly the CI constraint the
// existing tests already work around for qemu-img.
func fakeRunning(t *testing.T, v *config.VM) func() {
	return testutil.FakeRunning(t, v.Dir)
}

func TestListIncludesBrokenRatherThanOmittingThem(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "good", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 {
		t.Fatalf("List() = %d entries, want 2 (one good, one broken): %+v", len(vms), vms)
	}
	// Sorted by name: "good" < "hosed".
	if vms[0].Name != "good" || vms[0].State != StateStopped {
		t.Errorf("vms[0] = %+v, want good/stopped", vms[0])
	}
	if vms[1].Name != "hosed" || vms[1].State != StateBroken || vms[1].Error == "" {
		t.Errorf("vms[1] = %+v, want hosed/broken with a non-empty Error", vms[1])
	}
}

func TestGetKnownVM(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 2048, CPUs: 2, SSHPort: 2201}).Save(); err != nil {
		t.Fatal(err)
	}

	v, err := Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != StateStopped || v.OS != "alpine" || v.RAM != 2048 || v.SSHPort != 2201 {
		t.Errorf("Get(work) = %+v, want stopped/alpine/2048/2201", v)
	}
	// Paths must be populated from the real VM directory, not left zero.
	if v.Paths.Dir == "" || v.Paths.ConsoleLog == "" {
		t.Errorf("Paths not populated: %+v", v.Paths)
	}
}

func TestGetUnknownVM(t *testing.T) {
	root(t)
	if _, err := Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A broken vm.toml must come back as a VM the caller can see (StateBroken),
// not as a bare error indistinguishable from "no such VM", otherwise there
// is no way to ever offer deleting it.
func TestGetBrokenVMReturnsStateBrokenNotAnError(t *testing.T) {
	root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")

	v, err := Get("hosed")
	if err != nil {
		t.Fatalf("err = %v, want nil (broken is a state, not a failure)", err)
	}
	if v.State != StateBroken || v.Error == "" {
		t.Errorf("Get(hosed) = %+v, want StateBroken with a non-empty Error", v)
	}
}

func TestStartUnknownVM(t *testing.T) {
	root(t)
	if err := Start("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStartBrokenVM(t *testing.T) {
	root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")
	if err := Start("hosed"); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v, want ErrBroken", err)
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = filepath.Join(dir, "work")
	stop := fakeRunning(t, v)
	defer stop()

	if err := Start("work"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}

// down on a stopped VM is a failure, matching runDown's existing behaviour
// (internal/cli/cli.go): Stop must not silently succeed the way qemu.Stop
// does on its own.
func TestStopNotRunningIsAnError(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if err := Stop("work"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestStopUnknownVM(t *testing.T) {
	root(t)
	if err := Stop("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Destroy refuses while running, matching runRM and the TUI's delete key;
// see Destroy's doc comment for why stopping-first was rejected.
func TestDestroyRefusesWhileRunning(t *testing.T) {
	dir := root(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = filepath.Join(dir, "work")
	stop := fakeRunning(t, v)

	if err := Destroy("work"); !errors.Is(err, ErrAlreadyRunning) {
		stop()
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
	if _, err := os.Stat(v.Dir); err != nil {
		stop()
		t.Fatalf("VM directory should still exist after a refused destroy: %v", err)
	}
	stop()
	os.Remove(v.PidPath())

	if err := Destroy("work"); err != nil {
		t.Fatalf("Destroy after stopping: %v", err)
	}
	if _, err := os.Stat(v.Dir); !os.IsNotExist(err) {
		t.Fatalf("VM directory should be gone, got err = %v", err)
	}
}

func TestDestroyUnknownVM(t *testing.T) {
	root(t)
	if err := Destroy("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A broken VM's directory is still real and still deletable; this is the
// whole reason Get/List surface it instead of hiding it.
func TestDestroyBrokenVMDeletesTheDirectory(t *testing.T) {
	dir := root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")

	if err := Destroy("hosed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosed")); !os.IsNotExist(err) {
		t.Fatalf("broken VM directory should be gone, got err = %v", err)
	}
}

// TestIdentityFromListRoundTrips pins the rule that every `name string` in
// this package is a DIRECTORY name, and that VM.Name reports that directory
// rather than vm.toml's `name` field.
//
// The two diverge in practice: a VM's directory is fixed at creation and the
// file's name field can be edited afterwards, and every operation here is
// directory-anchored (config.Load builds Root()/<name>/vm.toml, qemu's pidfile
// comes off v.Dir, Delete removes v.Dir). Before this was fixed, a directory
// "work" holding `name = "work2"` came back from List as "work2", and feeding
// that straight back into Get returned ErrNotFound: the API handed out an
// identifier it would not itself accept. Worse than a failed lookup, had a
// real "work2" existed, the caller would have operated on the WRONG VM.
//
// internal/tui/list_test.go's TestDeleteTargetsDirectoryNotName is the same
// rule for the delete path; this is it for the core API's identity.
func TestIdentityFromListRoundTrips(t *testing.T) {
	root(t)
	writeRawVMToml(t, "work", "name = \"work2\"\nmode = \"live\"\nram = 512\ncpus = 1\nsshport = 2222\n")

	vms, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 1 {
		t.Fatalf("List() = %d entries, want 1: %+v", len(vms), vms)
	}
	if vms[0].Name != "work" {
		t.Fatalf("List()[0].Name = %q, want the DIRECTORY %q (vm.toml says \"work2\")", vms[0].Name, "work")
	}

	// The identifier List hands out must be one every other operation accepts.
	got, err := Get(vms[0].Name)
	if err != nil {
		t.Fatalf("Get(List()[0].Name) = %v; List handed out an identifier its own API rejects", err)
	}
	if got.Name != "work" {
		t.Fatalf("Get().Name = %q, want %q", got.Name, "work")
	}
	if _, err := SSHCommand(vms[0].Name); err != nil {
		t.Fatalf("SSHCommand(List()[0].Name) = %v; same identifier, different answer", err)
	}
}

// preOSFieldToml is a real example of a vm.toml written before `os` and
// `backend` existed as fields: exactly the ten keys a pre-registry Create
// wrote, no os/backend line among them.
const preOSFieldToml = `name = "test1"
mode = "live"
iso = "isos/alpine-standard-3.24.1-x86_64.iso"
ram = 4096
cpus = 2
disk = "8G"
installed = false
share = ""
sshport = 2222
recipes = []
`

// TestGetInfersOSAndBackendFromISOWhenFieldsAreEmpty is the bug in task 27:
// a vm.toml with no os/backend line reported "" for both, even though the
// ISO filename names the OS unambiguously.
func TestGetInfersOSAndBackendFromISOWhenFieldsAreEmpty(t *testing.T) {
	root(t)
	writeRawVMToml(t, "test1", preOSFieldToml)

	v, err := Get("test1")
	if err != nil {
		t.Fatal(err)
	}
	if v.OS != "alpine" {
		t.Errorf("OS = %q, want %q inferred from the iso filename", v.OS, "alpine")
	}
	// alpine-standard is a live ISO: apkovl, not the alpine-cloud override.
	if v.Backend != "apkovl" {
		t.Errorf("Backend = %q, want %q inferred from the iso filename", v.Backend, "apkovl")
	}
}

// TestGetNeverOverridesAnExplicitOS pins that vm.toml is the user's stated
// intent: an explicit os wins even when it disagrees with the iso filename,
// because a filename guess must never override what the file actually says.
func TestGetNeverOverridesAnExplicitOS(t *testing.T) {
	root(t)
	writeRawVMToml(t, "test1", `name = "test1"
mode = "live"
os = "debian"
backend = "ssh"
iso = "isos/alpine-standard-3.24.1-x86_64.iso"
ram = 4096
cpus = 2
sshport = 2222
`)

	v, err := Get("test1")
	if err != nil {
		t.Fatal(err)
	}
	if v.OS != "debian" {
		t.Errorf("OS = %q, want the explicit %q kept, not the iso filename's guess", v.OS, "debian")
	}
	if v.Backend != "ssh" {
		t.Errorf("Backend = %q, want the explicit %q kept", v.Backend, "ssh")
	}
}

// TestGetLeavesOSEmptyForAnUnrecognisableImage pins that inference is
// allowed to fail: a BYO image with no recognisable filename must not get a
// guessed OS, since a wrong guess is worse than an honest "unknown".
func TestGetLeavesOSEmptyForAnUnrecognisableImage(t *testing.T) {
	root(t)
	writeRawVMToml(t, "test1", `name = "test1"
mode = "live"
iso = "isos/disk.qcow2"
ram = 4096
cpus = 2
sshport = 2222
`)

	v, err := Get("test1")
	if err != nil {
		t.Fatal(err)
	}
	if v.OS != "" {
		t.Errorf("OS = %q, want empty: disk.qcow2 names no known OS", v.OS)
	}
}

// TestGetDoesNotModifyVMTomlOnDisk pins that inference happens only in
// memory: List/Get must stay read-only operations, since a backfill that
// rewrites vm.toml on read would turn `stoat ls` into a mutation.
func TestGetDoesNotModifyVMTomlOnDisk(t *testing.T) {
	dir := root(t)
	writeRawVMToml(t, "test1", preOSFieldToml)
	path := filepath.Join(dir, "test1", "vm.toml")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Get("test1"); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("vm.toml changed after Get/List:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestDestroyRefusesARunningBrokenVM pins a real bug: Destroy's broken-VM
// branch skipped the running check, on the stated but false grounds that
// qemu.Running needed a parsed config.VM. It needs only Dir, which that
// branch reconstructs, so a vm.toml corrupted AFTER its VM was started turned
// Destroy into a quiet way around the refusal that applies to every healthy
// VM, deleting the pidfile, monitor socket and disk from under a live qemu.
func TestDestroyRefusesARunningBrokenVM(t *testing.T) {
	root(t)
	// A directory that is running but whose vm.toml no longer parses.
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")
	stop := fakeRunning(t, &config.VM{Name: "hosed", Dir: filepath.Join(config.Root(), "hosed")})
	defer stop()

	if err := Destroy("hosed"); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Destroy on a running broken VM = %v, want ErrAlreadyRunning", err)
	}
	if _, err := os.Stat(filepath.Join(config.Root(), "hosed")); err != nil {
		t.Fatalf("the directory was deleted from under a running qemu: %v", err)
	}
}
