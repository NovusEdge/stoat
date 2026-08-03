package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

// ExecResult is what a command did, as data. A command that ran and exited
// non-zero is a RESULT, not a Go error: "the build failed with exit 1 and this
// on stderr" is the answer an agent asked for, and turning it into an error
// would force every caller to dig the same three fields back out of it. Exec
// returns an error only when the command could not be run at all.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command inside a VM and returns its output and exit status.
//
// ON EXIT STATUS 255: that is what ssh reports for its OWN errors — refused
// connection, failed handshake, unreachable host — and it is unavoidably
// ambiguous, because a remote command that itself exits 255 arrives through
// the same single channel. Exec does not pretend to resolve that. What it does
// is remove the common causes first: it refuses a VM that is not running, and
// sshx.Args already sets ConnectTimeout=5 and BatchMode=yes so a wedged
// network fails fast and a password prompt can never block forever. When 255
// does surface, ssh's own message is on Stderr, which is what distinguishes
// the two in practice.
//
// cmd is an ARGV, not a shell string, and each element is quoted for the
// guest's shell before being sent (see shellJoin). This matters more than it
// looks: ssh concatenates its trailing arguments with spaces and hands the
// result to the REMOTE shell to re-parse, so passing an argv through unquoted
// silently loses every word boundary — Exec(…, []string{"touch", "my file"})
// would create two files. An agent constructing commands from data it read
// somewhere is exactly the caller that would hit this and never notice.
//
// ctx cancels the ssh process. This is the one operation in this package that
// takes a context so far, and deliberately so rather than for symmetry: every
// other operation here is bounded by local work, while Exec runs an arbitrary
// command in a guest and has no other way to stop one that never returns.
//
// Exec does NOT gate on what the command is (docs/design/core-api.md §8,
// decision 1). It is a library call, and the TUI and CLI already let a user
// run anything; enforcement belongs in the MCP server, which is the boundary
// an agent actually crosses.
func Exec(ctx context.Context, name string, cmd []string) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("%w: no command given", ErrInvalidSpec)
	}
	v, err := load(name)
	if err != nil {
		return ExecResult{}, err
	}
	// Checked before dialling rather than letting ssh fail: a stopped VM is
	// the overwhelmingly common reason Exec cannot connect, and reporting it
	// as ErrNotRunning is both faster and far more useful than a bare 255
	// with "connection refused" buried in stderr.
	if !qemu.Running(v) {
		return ExecResult{}, fmt.Errorf("%w: %s", ErrNotRunning, name)
	}

	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, "ssh", sshx.Args(v, shellJoin(cmd))...)
	c.Stdout = &stdout
	c.Stderr = &stderr

	err = c.Run()
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}

	var ee *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &ee):
		// The command ran and exited non-zero. That is a result, not a
		// failure of Exec — see ExecResult.
		res.ExitCode = ee.ExitCode()
		// ctx expiring kills the process, which surfaces here as an
		// ExitError rather than as ctx.Err(). Report the cancellation: a
		// caller that cannot tell "timed out" from "the command failed"
		// will retry something that was never going to finish.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, fmt.Errorf("%s: %w", name, ctxErr)
		}
		return res, nil
	default:
		// ssh could not be started at all (not installed, not executable).
		// Nothing ran in the guest, so there is no exit status to report.
		return ExecResult{}, fmt.Errorf("%s: ssh: %w", name, err)
	}
}

// shellJoin renders an argv as a single string that the guest's shell will
// parse back into exactly those words.
//
// Every element is wrapped in single quotes, inside which POSIX shells treat
// every byte literally — no expansion, no globbing, no word splitting. The one
// byte that cannot appear inside single quotes is a single quote itself, which
// is closed, escaped and reopened as '\” — the standard idiom.
//
// This is deliberately NOT internal/installer's shellQuote: that one exists to
// write a value into a shell rc file for a HOST shell that may be fish, and
// this package must not import a front end anyway (see doctor.go). The rule
// they share is the reason both exist — a value that is not quoted is a value
// the shell rewrites.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
