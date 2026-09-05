package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/testutil"
)

// TestColorStateWidth pins the pad-then-colour order. The escape sequences
// are zero-width on screen but count toward a %-8s verb, so colouring first
// under-pads every coloured row by 9 columns and every field after STATE
// drifts left.
func TestColorStateWidth(t *testing.T) {
	for _, state := range []string{"running", "stopped", "broken"} {
		got := colorState(state, 8)
		if visible := stripANSI(got); len(visible) != 8 {
			t.Errorf("colorState(%q, 8) visible width = %d (%q), want 8", state, len(visible), visible)
		}
		// The coloured branch must wrap the *padded* text, not the bare state.
		if c := colorize(got, state); stripANSI(c) != stripANSI(got) {
			t.Errorf("colorize changed visible text: %q vs %q", stripANSI(c), stripANSI(got))
		}
	}
}

// stripANSI drops CSI colour sequences so a test can measure what the
// terminal actually shows.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    *Args
		wantErr bool
	}{
		{"no args", nil, nil, true},
		{"unknown subcommand", []string{"frobnicate"}, nil, true},

		{"ls", []string{"ls"}, &Args{Cmd: "ls"}, false},
		{"ls quiet", []string{"ls", "-q"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls --quiet", []string{"ls", "--quiet"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls --no-interactive", []string{"ls", "--no-interactive"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls unexpected arg", []string{"ls", "extra"}, nil, true},

		{"doctor", []string{"doctor"}, &Args{Cmd: "doctor"}, false},
		{"version", []string{"version"}, &Args{Cmd: "version"}, false},
		// help is asserted in TestParseHelpCarriesGeneratedText, not here: its
		// Args carries the whole generated help string, which this table
		// compares with DeepEqual and cannot sensibly hardcode.

		{"up", []string{"up", "alpine"}, &Args{Cmd: "up", VM: "alpine"}, false},
		// A missing name is a project-scope fan-out, not a parse error; see
		// resolveScope and TestMissingVMArgumentOutsideAProjectIsUsage.
		{"up missing name", []string{"up"}, &Args{Cmd: "up"}, false},
		{"up too many args", []string{"up", "a", "b"}, nil, true},
		{"up quiet", []string{"up", "-q", "alpine"}, &Args{Cmd: "up", VM: "alpine", Quiet: true}, false},
		{"up --no-apply", []string{"up", "--no-apply", "alpine"}, &Args{Cmd: "up", VM: "alpine", NoApply: true}, false},
		{"up --no-provision (hidden alias)", []string{"up", "--no-provision", "alpine"}, &Args{Cmd: "up", VM: "alpine", NoApply: true}, false},

		{"down", []string{"down", "alpine"}, &Args{Cmd: "down", VM: "alpine"}, false},
		{"down missing name", []string{"down"}, &Args{Cmd: "down"}, false},

		{"ssh", []string{"ssh", "alpine"}, &Args{Cmd: "ssh", VM: "alpine"}, false},
		{"ssh missing name", []string{"ssh"}, nil, true},

		// provision is a kong alias of apply, so it parses to Cmd "apply".
		{"provision", []string{"provision", "alpine"}, &Args{Cmd: "apply", VM: "alpine"}, false},
		{"provision missing name", []string{"provision"}, &Args{Cmd: "apply"}, false},
		{"provision quiet alias", []string{"provision", "--no-interactive", "alpine"}, &Args{Cmd: "apply", VM: "alpine", Quiet: true}, false},

		{"rm", []string{"rm", "alpine"}, &Args{Cmd: "rm", VM: "alpine"}, false},
		{"rm -y", []string{"rm", "-y", "alpine"}, &Args{Cmd: "rm", VM: "alpine", Yes: true}, false},
		// rm pulls the positional off before flag parsing, the same as
		// create and recipe new, so both orders work. See
		// TestParseRMAcceptsFlagsEitherSide.
		{"rm name then -y", []string{"rm", "alpine", "-y"}, &Args{Cmd: "rm", VM: "alpine", Yes: true}, false},
		{"rm two names", []string{"rm", "alpine", "extra"}, nil, true},
		{"rm missing name", []string{"rm"}, &Args{Cmd: "rm"}, false},
		{"rm quiet and yes", []string{"rm", "-q", "-y", "alpine"}, &Args{Cmd: "rm", VM: "alpine", Quiet: true, Yes: true}, false},

		{"logs default", []string{"logs"}, &Args{Cmd: "logs", N: 50, Which: core.WhichConsole}, false},
		{"logs -n", []string{"logs", "-n", "10"}, &Args{Cmd: "logs", N: 10, Which: core.WhichConsole}, false},
		// A positional is a VM name now, not a stray argument: `logs <vm>`
		// reads that VM's log. The no-name form is unchanged.
		{"logs vm name", []string{"logs", "work"}, &Args{Cmd: "logs", VM: "work", N: 50, Which: core.WhichConsole}, false},
		{"logs which apply", []string{"logs", "work", "--which", "apply"}, &Args{Cmd: "logs", VM: "work", N: 50, Which: core.WhichApply}, false},
		{"logs bad which", []string{"logs", "work", "--which", "bogus"}, nil, true},
		{"logs two names", []string{"logs", "work", "extra"}, nil, true},
		{"logs bad -n", []string{"logs", "-n", "notanumber"}, nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Parse(%v) = %+v, nil; want error", c.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%v) unexpected error: %v", c.args, err)
			}
			if !reflect.DeepEqual(got, c.want) { // reflect, not ==: Args embeds a core.Spec with a slice in it

				t.Fatalf("Parse(%v) = %+v; want %+v", c.args, got, c.want)
			}
		})
	}
}

// TestParseCreateAllowExecDefaultsTrue pins that a bare --allow-exec, with
// no value, still produces Spec.AllowExec pointing at true: AllowExec is a
// *bool with no kong default tag, so a bare flag relies on kong's ordinary
// bool parsing, not a default value. It also maps to the exec agent_access
// level, --allow-exec's alias contract.
func TestParseCreateAllowExecDefaultsTrue(t *testing.T) {
	got, err := Parse([]string{"create", "work", "--image", "alpine", "--allow-exec"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.AllowExec == nil || !*got.Spec.AllowExec {
		t.Errorf("create --allow-exec: Spec.AllowExec = %v, want a pointer to true", got.Spec.AllowExec)
	}
	if got.Spec.AgentAccess != "exec" {
		t.Errorf("create --allow-exec: Spec.AgentAccess = %q, want %q", got.Spec.AgentAccess, "exec")
	}
}

// TestParseCreateAllowExecOmittedDefaultsToManage pins that omitting both
// --allow-exec and --agent-access still gives Spec.AllowExec a pointer to
// true (see Spec.AllowExec's doc comment), while Spec.AgentAccess defaults
// to manage: only a given --allow-exec maps to exec.
func TestParseCreateAllowExecOmittedDefaultsToManage(t *testing.T) {
	got, err := Parse([]string{"create", "work", "--image", "alpine"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.AllowExec == nil || !*got.Spec.AllowExec {
		t.Errorf("create with no --allow-exec: Spec.AllowExec = %v, want a pointer to true", got.Spec.AllowExec)
	}
	if got.Spec.AgentAccess != "manage" {
		t.Errorf("create with no --allow-exec: Spec.AgentAccess = %q, want %q", got.Spec.AgentAccess, "manage")
	}
}

// TestParseCreateAllowExecFalse pins that --allow-exec=false is how a
// caller turns it off, mapping to the manage agent_access level.
func TestParseCreateAllowExecFalse(t *testing.T) {
	got, err := Parse([]string{"create", "work", "--image", "alpine", "--allow-exec=false"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.AllowExec == nil || *got.Spec.AllowExec {
		t.Errorf("create --allow-exec=false: Spec.AllowExec = %v, want a pointer to false", got.Spec.AllowExec)
	}
	if got.Spec.AgentAccess != "manage" {
		t.Errorf("create --allow-exec=false: Spec.AgentAccess = %q, want %q", got.Spec.AgentAccess, "manage")
	}
}

// TestParseCreateAgentAccessWinsOverAllowExec pins that an explicit
// --agent-access overrides the hidden --allow-exec alias, so a caller who
// passes both is not silently downgraded by the legacy flag.
func TestParseCreateAgentAccessWinsOverAllowExec(t *testing.T) {
	got, err := Parse([]string{"create", "work", "--image", "alpine", "--allow-exec", "--agent-access", "observe"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.AgentAccess != "observe" {
		t.Errorf("create --allow-exec --agent-access observe: Spec.AgentAccess = %q, want %q", got.Spec.AgentAccess, "observe")
	}
}

// TestParsePure guards against Parse doing anything beyond interpreting
// argv: it must never touch the filesystem, so calling it with a bogus
// STOAT_HOME must not error or panic.
func TestParsePure(t *testing.T) {
	t.Setenv("STOAT_HOME", "/nonexistent/definitely-not-there")
	if _, err := Parse([]string{"ls"}); err != nil {
		t.Fatalf("Parse must not touch disk, got error: %v", err)
	}
}

func TestMainExitCodes(t *testing.T) {
	var out, errOut discard

	if code := Main([]string{"help"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("help: exit %d, want %d", code, ExitOK)
	}
	if code := Main([]string{"version"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("version: exit %d, want %d", code, ExitOK)
	}
	if code := Main([]string{"bogus"}, "test", nil, &out, &errOut); code != ExitUsage {
		t.Fatalf("bogus subcommand: exit %d, want %d", code, ExitUsage)
	}
	if code := Main([]string{"up"}, "test", nil, &out, &errOut); code != ExitUsage {
		t.Fatalf("up with no name: exit %d, want %d", code, ExitUsage)
	}
}

// discard is a minimal io.Writer sink so tests don't need os.Stdout.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestParseRecipe covers the subcommand's argument shape. Flags must be
// parsed AFTER the positionals: Go's flag package stops at the first non-flag
// argument, so "recipe new x --os alpine" would otherwise silently ignore
// --os and report it as a stray argument.
func TestParseRecipe(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    *Args
		wantErr bool
	}{
		{"list", []string{"recipe", "list"}, &Args{Cmd: "recipe", Sub: "list"}, false},
		{
			"new with flags after the name",
			[]string{"recipe", "new", "nodejs", "--os", "alpine"},
			&Args{Cmd: "recipe", Sub: "new", VM: "nodejs", OS: "alpine"}, false,
		},
		{
			"new with a backend",
			[]string{"recipe", "new", "nodejs", "--os", "ubuntu", "--backend", "cloudinit"},
			&Args{Cmd: "recipe", Sub: "new", VM: "nodejs", OS: "ubuntu", Backend: "cloudinit"}, false,
		},
		{"new without a name", []string{"recipe", "new"}, nil, true},
		{"new with only a flag where the name goes", []string{"recipe", "new", "--os", "alpine"}, nil, true},
		{"unknown action", []string{"recipe", "frobnicate"}, nil, true},
		{"no action", []string{"recipe"}, nil, true},
		{"list with a stray argument", []string{"recipe", "list", "extra"}, nil, true},
		{"new with too many positionals", []string{"recipe", "new", "a", "b"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Parse(%v) = %+v, want an error", c.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%v): %v", c.args, err)
			}
			if !reflect.DeepEqual(got, c.want) { // reflect, not ==: Args embeds a core.Spec with a slice in it

				t.Errorf("Parse(%v) = %+v, want %+v", c.args, got, c.want)
			}
		})
	}
}

// Help text is generated from the kong grammar, so it cannot go stale; the
// failure mode this pins is empty text. `stoat help` used to set Cmd
// without rendering anything, working only through the -h FLAG path, and
// exit code 0 hid the bug.
func TestParseHelpCarriesGeneratedText(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		a, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if a.Cmd != "help" {
			t.Errorf("Parse(%v) Cmd = %q, want help", args, a.Cmd)
		}
		if a.Help == "" {
			t.Fatalf("Parse(%v) produced empty help text", args)
		}
		// Every subcommand has to appear, which is the property a
		// hand-written usage() string could not keep.
		for _, want := range []string{"ls", "create", "exec", "check-recipes", "ssh-command", "update"} {
			if !strings.Contains(a.Help, want) {
				t.Errorf("Parse(%v) help omits %q", args, want)
			}
		}
	}
}

// cliRoot points STOAT_HOME at a fresh temp dir for one test, exactly like
// core's own root(t) helper: reimplemented here rather than imported,
// since it lives in package core, not exported for reuse.
func cliRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STOAT_HOME", dir)
	return dir
}

// writeBrokenVMToml drops an unparseable vm.toml directly, bypassing
// config.VM.Save, so runLS/runUp/runDown/runRM's broken-VM paths are
// exercisable without a real broken VM ever having existed on disk.
func writeBrokenVMToml(t *testing.T, dir, name string) {
	t.Helper()
	vmDir := filepath.Join(dir, name)
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "vm.toml"), []byte("name = \""+name+"\"\nmode = \"disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeRunning marks v as running without a real qemu process: it spawns
// `sleep` with v.Dir in its argv (qemu.Running only checks that
// /proc/<pid>/cmdline contains dir+"/", not which binary produced it) and
// points the VM's pidfile at that pid. Mirrors internal/core/vm_test.go's
// helper of the same name and same trick, reimplemented here since it isn't
// exported.
func fakeRunning(t *testing.T, v *config.VM) func() {
	return testutil.FakeRunning(t, v.Dir)
}
func TestRunLSOutput(t *testing.T) {
	dir := cliRoot(t)
	if err := (&config.VM{Name: "good", Mode: "live", RAM: 1024, CPUs: 2, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	// Deliberately named to sort BEFORE "good", so a naive single-pass
	// core.List() print (no re-grouping) would show it first and this test
	// would catch that regression.
	writeBrokenVMToml(t, dir, "broken-vm")

	var out, errOut bytes.Buffer
	code := Main([]string{"ls"}, "test", nil, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("ls: exit %d, stderr %q", code, errOut.String())
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + good + broken): %q", len(lines), out.String())
	}
	wantHeader := fmt.Sprintf("%-15s %-5s %-8s %-5s %-6s %-6s %s", "NAME", "MODE", "STATE", "CPUS", "RAM", "SSH", "PROJECT")
	if lines[0] != wantHeader {
		t.Errorf("header = %q, want %q", lines[0], wantHeader)
	}
	wantGood := fmt.Sprintf("%-15s %-5s %s %-5d %-6d %-6d %s", "good", "live", "stopped ", 2, 1024, 2200, "-")
	if lines[1] != wantGood {
		t.Errorf("good row = %q, want %q", lines[1], wantGood)
	}
	wantBrokenPrefix := fmt.Sprintf("%-15s %-5s %s %-5s %-6s %-4s ", "broken-vm", "-", "broken  ", "-", "-", "-")
	if !strings.HasPrefix(lines[2], wantBrokenPrefix) {
		t.Errorf("broken row = %q, want prefix %q", lines[2], wantBrokenPrefix)
	}
}

// runDown must refuse before printing its "stopping..." progress line, not
// after, matching the pre-core behaviour where the running/not-running
// check happened before anything was written to stdout.
func TestRunDownNotRunning(t *testing.T) {
	cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"down", "work"}, "test", nil, &out, &errOut)
	if code != ExitFail {
		t.Fatalf("down: exit %d, want %d", code, ExitFail)
	}
	if want := "stoat: down: work is not running\n"; errOut.String() != want {
		t.Errorf("errOut = %q, want %q", errOut.String(), want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty (no progress line on a refusal)", out.String())
	}
}

func TestRunDownUnknownVM(t *testing.T) {
	cliRoot(t)
	var out, errOut bytes.Buffer
	code := Main([]string{"down", "ghost"}, "test", nil, &out, &errOut)
	if code != ExitFail {
		t.Fatalf("down: exit %d, want %d", code, ExitFail)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

// runRM must refuse a running VM before the delete confirmation prompt even
// shows, matching pre-core behaviour exactly (a running VM was never asked
// "delete? [y/N]": it just failed).
func TestRunRMRunningRefusesBeforePrompting(t *testing.T) {
	dir := cliRoot(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	v.Dir = filepath.Join(dir, "work")
	stop := fakeRunning(t, v)
	defer stop()

	var out, errOut bytes.Buffer
	// runRM directly, not Main(): fakeRunning's stand-in process is real (see
	// its doc comment) and only "running" for as long as it survives. A test
	// sandbox can reap it during Main()'s setup work before the check runs.
	// Calling runRM immediately keeps that gap effectively zero.
	// -y proves the refusal happens before the confirmation gate, not
	// because of it.
	code := runRM(&Args{Cmd: "rm", VM: "work", Yes: true}, nil, &out, &errOut)
	if code != ExitFail {
		t.Fatalf("rm: exit %d, want %d", code, ExitFail)
	}
	if want := "stoat: rm: work is running; stop it first\n"; errOut.String() != want {
		t.Errorf("errOut = %q, want %q", errOut.String(), want)
	}
	if _, err := os.Stat(v.Dir); err != nil {
		t.Errorf("VM directory should survive a refused rm: %v", err)
	}
}

func TestRunRMDeletesAStoppedVM(t *testing.T) {
	dir := cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code := Main([]string{"rm", "-y", "work"}, "test", nil, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("rm: exit %d, stderr %q", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "work")); !os.IsNotExist(err) {
		t.Errorf("VM directory should be gone, got err = %v", err)
	}
}

// TestParseRMAcceptsFlagsEitherSide pins that `rm <name> -y` and `rm -y
// <name>` both work.
func TestParseRMAcceptsFlagsEitherSide(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "work", "-y"},
		{"rm", "-y", "work"},
	} {
		got, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) errored: %v", args, err)
		}
		if got.VM != "work" || !got.Yes {
			t.Fatalf("Parse(%v) = VM %q, Yes %v; want work, true", args, got.VM, got.Yes)
		}
	}

	// A second positional is still a usage error either way round.
	for _, args := range [][]string{
		{"rm", "work", "extra"},
		{"rm", "-y", "work", "extra"},
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) accepted a second vm name", args)
		}
	}
}

// TestParseExecTakesTheCommandVerbatim pins that exec does no flag parsing of
// its own. `stoat exec work ls -la` must send -la to ls. If stoat's flag
// package ever gets hold of the command, a guest command with a flag in it
// silently runs the wrong thing instead of failing.
func TestParseExecTakesTheCommandVerbatim(t *testing.T) {
	cases := []struct {
		args []string
		vm   string
		cmd  []string
	}{
		{[]string{"exec", "work", "ls"}, "work", []string{"ls"}},
		{[]string{"exec", "work", "ls", "-la", "/tmp"}, "work", []string{"ls", "-la", "/tmp"}},
		// Flags that stoat itself defines must NOT be swallowed.
		{[]string{"exec", "work", "sh", "-c", "echo hi"}, "work", []string{"sh", "-c", "echo hi"}},
		{[]string{"exec", "work", "-q"}, "work", []string{"-q"}},
		{[]string{"exec", "work", "grep", "-y", "pattern"}, "work", []string{"grep", "-y", "pattern"}},
		// A leading -- is optional and dropped.
		{[]string{"exec", "work", "--", "ls", "-la"}, "work", []string{"ls", "-la"}},
	}
	for _, c := range cases {
		got, err := Parse(c.args)
		if err != nil {
			t.Fatalf("Parse(%v) errored: %v", c.args, err)
		}
		if got.VM != c.vm || !reflect.DeepEqual(got.Command, c.cmd) {
			t.Errorf("Parse(%v) = VM %q cmd %q; want %q %q", c.args, got.VM, got.Command, c.vm, c.cmd)
		}
	}

	for _, args := range [][]string{{"exec"}, {"exec", "work"}, {"exec", "work", "--"}} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) was accepted; want a usage error", args)
		}
	}
}

// TestParseCPInfersDirection pins that `stoat cp` works out which way the file
// is going from which side carries the "<vm>:" prefix, so there is no
// --to/--from flag for a user to get backwards. Both-sides and neither-side
// are usage errors rather than a guess.
func TestParseCPInfersDirection(t *testing.T) {
	got, err := Parse([]string{"cp", "/host/a.txt", "work:/tmp/a.txt"})
	if err != nil {
		t.Fatalf("cp to guest: %v", err)
	}
	if got.VM != "work" || got.Local != "/host/a.txt" || got.Remote != "/tmp/a.txt" || !got.ToRemote {
		t.Errorf("cp to guest = %+v", got)
	}

	got, err = Parse([]string{"cp", "work:/tmp/a.txt", "/host/a.txt"})
	if err != nil {
		t.Fatalf("cp from guest: %v", err)
	}
	if got.VM != "work" || got.Local != "/host/a.txt" || got.Remote != "/tmp/a.txt" || got.ToRemote {
		t.Errorf("cp from guest = %+v", got)
	}

	for _, args := range [][]string{
		{"cp", "a:/x", "b:/y"}, // guest to guest
		{"cp", "/x", "/y"},     // host to host: that is just cp
		{"cp", "work:/x"},      // one argument
		{"cp", "a", "b", "c"},  // three
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) was accepted; want a usage error", args)
		}
	}
}

// TestParseCPFlagForm pins the explicit-flag spelling docs/design/mcp-server.md
// §1.1 adds alongside the positional one: --direction picks the way, --local
// and --remote are unambiguous regardless of what characters either path
// contains, unlike the positional form's "<vm>:<path>" compound.
func TestParseCPFlagForm(t *testing.T) {
	got, err := Parse([]string{"cp", "--vm", "work", "--direction", "to", "--local", "/host/a.txt", "--remote", "/tmp/a.txt"})
	if err != nil {
		t.Fatalf("cp --direction to: %v", err)
	}
	if got.VM != "work" || got.Local != "/host/a.txt" || got.Remote != "/tmp/a.txt" || !got.ToRemote {
		t.Errorf("cp --direction to = %+v", got)
	}

	got, err = Parse([]string{"cp", "--vm", "work", "--direction", "from", "--remote", "/tmp/a.txt", "--local", "/host/a.txt"})
	if err != nil {
		t.Fatalf("cp --direction from: %v", err)
	}
	if got.VM != "work" || got.Local != "/host/a.txt" || got.Remote != "/tmp/a.txt" || got.ToRemote {
		t.Errorf("cp --direction from = %+v", got)
	}
}

// TestParseCPFormsAreMutuallyExclusive pins that giving both the positional
// arguments and the flags is rejected rather than one silently winning.
func TestParseCPFormsAreMutuallyExclusive(t *testing.T) {
	_, err := Parse([]string{"cp", "/host/a.txt", "work:/tmp/a.txt", "--vm", "work", "--direction", "to", "--local", "/host/a.txt", "--remote", "/tmp/a.txt"})
	if err == nil {
		t.Fatal("expected a usage error mixing positionals and flags")
	}
}

// TestParseCPFlagFormRejectsPartialFlags pins that --vm alone (or any subset
// short of all four) is a usage error, not a request that silently falls
// back to defaults for the rest.
func TestParseCPFlagFormRejectsPartialFlags(t *testing.T) {
	for _, args := range [][]string{
		{"cp", "--vm", "work"},
		{"cp", "--vm", "work", "--direction", "to"},
		{"cp", "--vm", "work", "--direction", "to", "--local", "/host/a.txt"},
		{"cp", "--direction", "to", "--local", "/host/a.txt", "--remote", "/tmp/a.txt"}, // no --vm
	} {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%v) was accepted; want a usage error for an incomplete flag set", args)
		}
	}
}

// TestParseCPDirectionEnumRejectsGarbage pins that --direction is a real
// enum, not a bare string smuggled through: kong's enum tag must reject
// anything besides "to"/"from" before toArgs ever sees it.
func TestParseCPDirectionEnumRejectsGarbage(t *testing.T) {
	_, err := Parse([]string{"cp", "--vm", "work", "--direction", "sideways", "--local", "/host/a.txt", "--remote", "/tmp/a.txt"})
	if err == nil {
		t.Fatal("expected a usage error for an invalid --direction")
	}
}

// TestParseCPResolvesLocalToAbsolutePath pins that a relative path, or one
// with a leading ~, resolves to the RESOLVED absolute path in a.Local
// (docs/design/mcp-server.md §1.1): runCopy echoes it back on the wire for
// the server to post-verify. Both the positional and flag forms resolve the
// same way.
func TestParseCPResolvesLocalToAbsolutePath(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got, err := Parse([]string{"cp", "relative/a.txt", "work:/tmp/a.txt"})
	if err != nil {
		t.Fatalf("positional relative path: %v", err)
	}
	want := filepath.Join(cwd, "relative/a.txt")
	if got.Local != want {
		t.Errorf("positional form: Local = %q, want %q", got.Local, want)
	}

	got, err = Parse([]string{"cp", "--vm", "work", "--direction", "to", "--local", "~/a.txt", "--remote", "/tmp/a.txt"})
	if err != nil {
		t.Fatalf("flag form tilde path: %v", err)
	}
	want = filepath.Join(home, "a.txt")
	if got.Local != want {
		t.Errorf("flag form: Local = %q, want %q", got.Local, want)
	}
}
