package core

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// TestShellJoinAgainstARealShell runs shellJoin's output through an actual
// shell and checks the words that come back are the words that went in.
//
// A hand-written unquoter would only be as correct as the author's
// understanding of shell quoting, the thing under test. That makes it a
// tautology. internal/installer/paths_shell_test.go uses the same
// approach for the same reason and skips shells that are not installed,
// so a missing host tool never reddens CI.
//
// The guest shell is /bin/sh or /bin/ash on Alpine, so POSIX sh is the
// right target; see guest.OS.Shell.
func TestShellJoinAgainstARealShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}

	cases := [][]string{
		{"echo", "hello"},
		{"touch", "my file"},                       // the word-splitting case
		{"echo", "it's"},                           // the one byte single quotes cannot hold
		{"echo", "a'b'c"},                          //  ... more than once
		{"echo", "$HOME"},                          // must NOT expand
		{"echo", "`whoami`"},                       // must NOT execute
		{"echo", "$(whoami)"},                      // ... nor this form
		{"echo", "*"},                              // must NOT glob
		{"echo", "a b", "c\td"},                    // multiple args, mixed whitespace
		{"echo", "semi;colon", "pipe|bar", "amp&"}, // shell metacharacters
		{"echo", "new\nline"},                      // embedded newline
		{"echo", `back\slash`},                     // backslash stays literal
		{"echo", `"double"`},                       // double quotes stay literal
		{"echo", ""},                               // an empty argument must survive
	}

	for _, argv := range cases {
		// Arguments are separated by NUL, not newline, so the shell reports
		// exactly how many words it parsed and what each one was. A
		// newline delimiter cannot express an argument that itself
		// contains a newline. An earlier version of this test used one and
		// reported a spurious failure on {"echo", "new\nline"}. NUL is the
		// only byte that cannot appear inside an argument; that is why
		// xargs -0 and find -print0 use it. printf with a repeated format
		// consumes every remaining argument.
		script := `printf '%s\0' ` + shellJoin(argv[1:])
		out, err := exec.Command(sh, "-c", script).Output()
		if err != nil {
			t.Errorf("sh -c %q failed: %v", script, err)
			continue
		}

		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		want := argv[1:]
		if len(got) != len(want) {
			t.Errorf("shellJoin(%q): shell parsed %d words %q, want %d %q",
				want, len(got), got, len(want), want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("shellJoin(%q): word %d came back as %q, want %q",
					want, i, got[i], want[i])
			}
		}
	}
}

// TestShellJoinNeverLeavesAnArgumentBare is a structural guard on top of
// the behavioral test above. Every argument must be quoted, including ones
// that look harmless today. A "no special characters, skip the quotes"
// optimization is how this bug gets reintroduced.
func TestShellJoinNeverLeavesAnArgumentBare(t *testing.T) {
	got := shellJoin([]string{"ls", "-la", "/tmp"})
	if got != `'ls' '-la' '/tmp'` {
		t.Errorf("shellJoin = %q, want every word quoted", got)
	}
}

func TestExecRejectsAnEmptyCommand(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Exec(context.Background(), "work", nil); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func TestExecUnknownVM(t *testing.T) {
	root(t)
	if _, err := Exec(context.Background(), "nope", []string{"true"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A stopped VM must be refused before ssh is ever dialled. Otherwise the
// caller waits out ConnectTimeout only to get a bare 255 with "connection
// refused" buried in stderr, which says nothing about the actual problem.
func TestExecStoppedVMIsNotRunningNotATransportError(t *testing.T) {
	root(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 512, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	res, err := Exec(context.Background(), "work", []string{"true"})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
	if res.ExitCode != 0 || res.Stdout != "" || res.Stderr != "" {
		t.Errorf("got a populated result %+v for a command that never ran", res)
	}
}

func TestExecBrokenVM(t *testing.T) {
	root(t)
	writeRawVMToml(t, "hosed", "name = \"hosed\"\nmode = \"disk\n")
	if _, err := Exec(context.Background(), "hosed", []string{"true"}); !errors.Is(err, ErrBroken) {
		t.Fatalf("err = %v, want ErrBroken", err)
	}
}
