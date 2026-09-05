package cli

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// afterStart is exercised directly rather than through runUp, which would
// need core.Start to actually launch qemu. fakeRunning marks the VM as
// running the same way apply_test.go does, so afterStart's own decision
// (core.NeedsProvision, --no-apply) is tested without a real boot or a real
// ssh wait.
func startedVM(t *testing.T, name string, patch func(*config.VM)) core.VM {
	t.Helper()
	dir := cliRoot(t)
	v := &config.VM{Name: name, Mode: "live", OS: "alpine", RAM: 512, CPUs: 1, SSHPort: 2200}
	if patch != nil {
		patch(v)
	}
	saveVM(t, v)
	v.Dir = filepath.Join(dir, name)
	t.Cleanup(fakeRunning(t, v))

	cv, err := core.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return cv
}

// TestAfterStartSkipsWhenNothingPending: a VM with no recipes and no share
// has nothing for a provision run to do, so `up` must return without
// waiting for ssh.
func TestAfterStartSkipsWhenNothingPending(t *testing.T) {
	v := startedVM(t, "work", nil)

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work"}
	code := afterStart(a, v, &out, &errOut)

	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "applying recipes") || strings.Contains(out.String(), "waiting for ssh") {
		t.Errorf("waited on a VM with nothing pending: %q", out.String())
	}
}

// TestAfterStartNoApplySkipsEvenWithRecipesPending: --no-apply must win over
// core.NeedsProvision, not just apply when there is nothing to do.
func TestAfterStartNoApplySkipsEvenWithRecipesPending(t *testing.T) {
	v := startedVM(t, "work", func(v *config.VM) {
		v.Recipes = []string{"xfce.alpine.sh"}
	})

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work", NoApply: true}
	code := afterStart(a, v, &out, &errOut)

	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "applying recipes") || strings.Contains(out.String(), "waiting for ssh") {
		t.Errorf("--no-apply did not skip the apply: %q", out.String())
	}
}

// assertJSONCleanStdout fails the test if any non-empty line of out is not a
// JSON object, pinning docs/reference/json.md rule 1: every line of stdout
// under --json is one JSON object, never prose. It accepts zero lines too,
// since a caller that emits its terminal result elsewhere is still clean.
func assertJSONCleanStdout(t *testing.T, out string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("stdout line is not JSON under --json: %v\nline: %q\nfull stdout: %q", err, line, out)
		}
	}
}

// TestAfterStartJSONWaitsForApplyToFinish pins the fix for "up --json
// returns before the boot-time apply finishes": under --json, afterStart
// must still wait for ssh and run Apply, not skip straight to ExitOK, and
// none of that work may print plain prose or raw apply-log bytes to stdout
// (docs/reference/json.md rule 1; the apply log is wrapped as "log" events
// under --json, via the same jsonLogWriter run_apply.go uses). config.Load's
// Applied entry is the proof Apply actually ran rather than the call
// returning early.
//
// Quiet is set alongside JSON because Main always sets it that way
// (internal/cli/cli.go:426): the two "waiting for ssh"/"applying recipes"
// headers are already gated on it, so this isolates the two prints that are
// NOT gated by Quiet at all, "%s: recipes applied" and the streamed log
// content, as the actual gap in the --json contract.
func TestAfterStartJSONWaitsForApplyToFinish(t *testing.T) {
	dir := cliRoot(t)
	writeV2Recipe(t, dir, "tool", "once", "1.0", "#!/bin/sh\necho applied\n")
	installFakeSSHClient(t)
	port, stopSSHD := fakeSSHD(t, 0)
	defer stopSSHD()

	v := &config.VM{
		Name: "work", Mode: "cloud", OS: "alpine", Backend: "cloudinit",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"tool"},
	}
	saveVM(t, v)
	v.Dir = filepath.Join(dir, "work")
	t.Cleanup(fakeRunning(t, v))

	cv, err := core.Get("work")
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work", JSON: true, Quiet: true}
	code := afterStart(a, cv, &out, &errOut)

	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK: stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	assertJSONCleanStdout(t, out.String())
	if !strings.Contains(out.String(), `"type":"log"`) {
		t.Errorf("stdout has no log event; recipe output was dropped instead of wrapped: %q", out.String())
	}

	updated, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.Applied["tool"]; !ok {
		t.Errorf("afterStart returned before Apply ran: %q never recorded in Applied", "tool")
	}
}

// writeV2Recipe writes a recipe.toml plus its script directly under
// rootDir/recipes/name, mirroring internal/core/apply_test.go's helper of
// the same name: the recipe loader reads that directory with no project or
// lock file needed.
func writeV2Recipe(t *testing.T, rootDir, name, run, version, script string) {
	t.Helper()
	recipeDir := filepath.Join(rootDir, "recipes", name)
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "name = \"" + name + "\"\n" +
		"version = \"" + version + "\"\n" +
		"script = \"install.sh\"\n" +
		"run = \"" + run + "\"\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// installFakeSSHClient puts a stand-in "ssh" on PATH ahead of the real one,
// mirroring internal/core/apply_reboot_test.go's helper of the same name. It
// discards stdin (a recipe run pipes the script over it) and exits 0, so a
// real recipe body never has to run.
func installFakeSSHClient(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// fakeSSHD listens on 127.0.0.1, on port, or a free one if port is 0, and
// answers every connection with a real SSH identification banner, mirroring
// internal/core/wait_test.go's helper of the same name: sshBannerUp's "SSH-"
// check passes without a real sshd installed.
func fakeSSHD(t *testing.T, port int) (int, func()) {
	t.Helper()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = c.Write([]byte("SSH-2.0-fake\r\n"))
				<-done
				_ = c.Close()
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() {
		close(done)
		_ = l.Close()
	}
}
