package cli

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// TestWaitMissingVM covers the ordinary core.Get-style not_found path:
// core.Wait's own load() call is what produces it, before any polling starts.
func TestWaitMissingVM(t *testing.T) {
	cliRoot(t)
	code, objs := runJSON(t, "wait", "ghost")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	res := result(t, objs)
	errObj, _ := res["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeNotFound) {
		t.Errorf("error.code = %v, want %q", errObj["code"], wire.CodeNotFound)
	}
}

// TestWaitCannotReach exercises the impossible-by-construction case
// core.Wait catches up front (see waitReachable in internal/core/wait.go): a
// stopped VM can never bring up sshd on its own, so this must fail fast with
// cannot_reach rather than block for the timeout.
func TestWaitCannotReach(t *testing.T) {
	cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	code, objs := runJSON(t, "wait", "work", "--until", "reachable", "--timeout", "5s")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	res := result(t, objs)
	errObj, _ := res["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeCannotReach) {
		t.Errorf("error.code = %v, want %q", errObj["code"], wire.CodeCannotReach)
	}
}

// TestWaitTimeout is a real ctx deadline: work is made to look running (see
// fakeRunning), so waitStopped polls forever and the 50ms --timeout is what
// actually ends the command. This is also what proves core.Wait's timeout
// still carries the context.DeadlineExceeded sentinel through to wire.MapError:
// pollUntil returns ctx.Err() itself, no wrapping needed.
func TestWaitTimeout(t *testing.T) {
	dir := cliRoot(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = dir + "/work"
	stop := fakeRunning(t, v)
	defer stop()

	code, objs := runJSON(t, "wait", "work", "--until", "stopped", "--timeout", "50ms")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	res := result(t, objs)
	errObj, _ := res["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeTimeout) {
		t.Errorf("error.code = %v, want %q", errObj["code"], wire.CodeTimeout)
	}
}

// TestParseWaitUsageErrors covers the two Parse-level rejections: an unknown
// --until and a non-positive --timeout, both exit-2 usage errors before
// core.Wait is ever called.
func TestParseWaitUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"wait", "work", "--until", "bogus"},
		{"wait", "work", "--timeout", "0"},
		{"wait", "work", "--healthy", "--until", "reachable"},
		{"wait", "work", "--healthy", "--until", "applied"},
		{"wait", "work", "--healthy", "--until", "stopped"},
		{"wait", "work", "--until", "reachable", "--healthy"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) accepted, want a usage error", args)
		}
	}
}

func TestParseWaitHealthySelectsTheHealthEvent(t *testing.T) {
	a, err := Parse([]string{"wait", "work", "--healthy", "--timeout", "7s"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Until != core.UntilHealthy {
		t.Fatalf("Until = %q, want %q", a.Until, core.UntilHealthy)
	}
	if a.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %s, want 7s", a.Timeout)
	}
}

func TestWaitUsageErrorsUnderJSON(t *testing.T) {
	cliRoot(t)
	for _, args := range [][]string{
		{"wait", "work", "--until", "bogus"},
		{"wait", "work", "--timeout", "0"},
	} {
		code, objs := runJSON(t, args...)
		if code != ExitUsage {
			t.Errorf("Main(%v) exit = %d, want %d", args, code, ExitUsage)
		}
		res := result(t, objs)
		errObj, _ := res["error"].(map[string]any)
		if errObj["code"] != string(wire.CodeUsage) {
			t.Errorf("Main(%v) error.code = %v, want %q", args, errObj["code"], wire.CodeUsage)
		}
	}
}

// A health check may have observed a real failing verdict, but a caller's
// shorter JSON timeout still owns the boundary. The result must remain the
// machine-readable timeout rather than exposing the intermediate failure.
func TestWaitHealthyJSONDeadlineAfterObservedFailureIsTimeout(t *testing.T) {
	dir := cliRoot(t)
	recipeDir := filepath.Join(dir, "recipes", "healthy-timeout")
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"healthy-timeout\"\nscript = \"install.sh\"\n\n[health]\ncheck = \"docker info\"\ntimeout = \"2s\"\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	sshScript := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' 'health still failing' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(sshScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	port, stopSSH := cliFakeSSHD(t)
	defer stopSSH()
	v := &config.VM{
		Name: "work", Mode: "live", OS: "alpine", RAM: 1024, CPUs: 1,
		SSHPort: port, Recipes: []string{"healthy-timeout"},
		Applied: map[string]config.AppliedRecipe{"healthy-timeout": {}},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()

	code, objs := runJSON(t, "wait", "work", "--healthy", "--timeout", "100ms")
	if code != ExitFail {
		t.Fatalf("exit = %d, want %d", code, ExitFail)
	}
	res := result(t, objs)
	errObj, _ := res["error"].(map[string]any)
	if errObj["code"] != string(wire.CodeTimeout) {
		t.Fatalf("error.code = %v, want %q after observed health failure", errObj["code"], wire.CodeTimeout)
	}
}

func cliFakeSSHD(t *testing.T) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				_, _ = conn.Write([]byte("SSH-2.0-fake\r\n"))
				<-done
				_ = conn.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() {
		close(done)
		_ = l.Close()
	}
}
