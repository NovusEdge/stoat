package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// vm writes a minimal, real vm.toml under a fresh STOAT_HOME and returns it.
// Loaded back through config.Load rather than handed to the functions under
// test directly, so these tests exercise the same disk round-trip a real
// caller gets.
func vm(t *testing.T, name, sshUser string) *config.VM {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &config.VM{
		Name:    name,
		Mode:    "live",
		OS:      "alpine",
		Backend: "apkovl",
		SSHPort: 2222,
		SSHUser: sshUser,
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSSHCommandRoot(t *testing.T) {
	v := vm(t, "work", "")

	argv, err := SSHCommand(v.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) == 0 || argv[0] != "ssh" {
		t.Fatalf("argv[0] = %v, want \"ssh\" as element 0", argv)
	}
	if !containsPair(argv, "-p", "2222") {
		t.Errorf("argv %v missing -p 2222", argv)
	}
	if !containsSuffix(argv, "root@127.0.0.1") {
		t.Errorf("argv %v missing root@127.0.0.1 for an empty SSHUser", argv)
	}
}

func TestSSHCommandSetUser(t *testing.T) {
	v := vm(t, "cloudy", "stoat")

	argv, err := SSHCommand(v.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSuffix(argv, "stoat@127.0.0.1") {
		t.Errorf("argv %v missing stoat@127.0.0.1 for SSHUser=stoat", argv)
	}
}

func TestSSHCommandUnknownVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if _, err := SSHCommand("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A broken vm.toml has no SSHPort and no SSHUser to build an argv from;
// see SSHCommand's doc comment for why ErrBroken, not ErrNotFound, is the
// right answer here, unlike Logs below.
func TestSSHCommandBrokenVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeBrokenVMToml(t, "hosed")

	if _, err := SSHCommand("hosed"); !errors.Is(err, ErrBroken) {
		t.Errorf("err = %v, want ErrBroken", err)
	}
}

func TestLogsReturnsWrittenBytes(t *testing.T) {
	v := vm(t, "work", "")

	if err := os.WriteFile(v.ConsoleLogPath(), []byte("console output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.ProvisionLogPath(), []byte("apply output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Logs(v.Name, WhichConsole)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "console output\n" {
		t.Errorf("console log = %q", b)
	}

	r, err = Logs(v.Name, WhichApply)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err = io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "apply output\n" {
		t.Errorf("apply log = %q", b)
	}
}

// A VM that was never started or provisioned has neither log file. That is
// normal, not an error (see Logs' doc comment), so this must come back as
// an empty, already-EOF reader rather than a failure.
func TestLogsMissingFileIsEmptyNotError(t *testing.T) {
	v := vm(t, "fresh", "")

	r, err := Logs(v.Name, WhichConsole)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Errorf("got %q, want empty for a missing log file", b)
	}
}

func TestLogsUnknownWhich(t *testing.T) {
	v := vm(t, "work", "")

	if _, err := Logs(v.Name, Which("bogus")); err == nil {
		t.Error("want an error for an unknown Which, got nil")
	}
}

func TestLogsUnknownVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if _, err := Logs("nope", WhichConsole); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// Logs on a broken VM must still serve the console log; see Logs' doc
// comment. The file lives in the VM directory and does not depend on
// vm.toml parsing. A broken VM is exactly the case where a user most wants
// to read it.
func TestLogsBrokenVMStillServesConsoleLog(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	dir := writeBrokenVMToml(t, "hosed")
	if err := os.WriteFile(filepath.Join(dir, "console.log"), []byte("boot output\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := Logs("hosed", WhichConsole)
	if err != nil {
		t.Fatalf("Logs on a broken VM should still serve the console log, got err = %v", err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "boot output\n" {
		t.Errorf("console log = %q", b)
	}
}

// Same as TestLogsMissingFileIsEmptyNotError, but for a broken VM: no log
// file was ever written, so this must still be an empty reader, not an
// error layered on top of ErrBroken.
func TestLogsBrokenVMMissingFileIsEmptyNotError(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeBrokenVMToml(t, "hosed")

	r, err := Logs("hosed", WhichConsole)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Errorf("got %q, want empty for a broken VM with no console.log", b)
	}
}

// writeBrokenVMToml writes an unparseable vm.toml directly, bypassing
// config.VM.Save, so SSHCommand and Logs's ErrBroken path is testable. It is
// a separate declaration from vm_test.go's writeRawVMToml to avoid a name
// collision between the two files in package core.
func writeBrokenVMToml(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(config.Root(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte("name = \""+name+"\"\nmode = \"disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Small local helpers, kept out of the errors/strings packages' way of an
// import cycle, with nothing else in this file needing them.

func containsPair(argv []string, flag, val string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == flag && argv[i+1] == val {
			return true
		}
	}
	return false
}

func containsSuffix(argv []string, s string) bool {
	for _, a := range argv {
		if a == s {
			return true
		}
	}
	return false
}
