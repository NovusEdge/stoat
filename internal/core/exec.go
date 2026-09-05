package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/novusedge/stoat/internal/qemu"
	"github.com/novusedge/stoat/internal/sshx"
)

// ExecResult is what a command did, as data. A command that ran and exited
// non-zero is a result, not a Go error: "exit 1, this on stderr" is the
// answer a caller asked for. Turning it into an error would force every
// caller to dig the same three fields back out. Exec returns an error only
// when the command could not run at all.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a command inside a VM and returns its output and exit status.
//
// Exit status 255 is ambiguous. ssh itself reports 255 for its own errors
// (refused connection, failed handshake, unreachable host), and a remote
// command that exits 255 arrives through the same channel. Exec does not
// resolve that ambiguity. It removes the common causes instead: it refuses
// a VM that is not running, and sshx.Args sets ConnectTimeout=5 and
// BatchMode=yes so a wedged network fails fast and a password prompt never
// blocks forever. When 255 does surface, ssh's own message is on Stderr.
//
// cmd is an argv, not a shell string. sshx.Run quotes each element for the
// guest's shell before sending it. ssh concatenates its trailing arguments
// with spaces and hands the result to the remote shell to re-parse, so an
// unquoted argv silently loses every word boundary.
//
// ctx cancels the ssh process. Exec is the only operation in this package
// that takes a context: every other operation is bounded by local work,
// while Exec runs an arbitrary command in a guest with no other way to
// stop one that never returns.
//
// Exec does not gate on what the command is (docs/design/core-api.md §8,
// decision 1). It is a library call; the TUI and CLI already let a user
// run anything. Enforcement belongs in the MCP server, the boundary an
// agent actually crosses.
func Exec(ctx context.Context, name string, cmd []string) (ExecResult, error) {
	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("%w: no command given", ErrInvalidSpec)
	}
	v, err := load(name)
	if err != nil {
		return ExecResult{}, err
	}
	// Checked before dialling, not left for ssh to fail on. A stopped VM
	// is the most common reason Exec cannot connect. ErrNotRunning is
	// faster and clearer than a bare 255 with "connection refused" buried
	// in stderr.
	if !qemu.Running(v) {
		return ExecResult{}, fmt.Errorf("%w: %s", ErrNotRunning, name)
	}

	out, errb, code, err := sshx.Run(ctx, v, false, cmd, nil)
	res := ExecResult{Stdout: string(out), Stderr: string(errb), ExitCode: code}
	if err != nil {
		// ctx expiring kills the ssh process and surfaces here as a plain
		// exit error, not as ctx.Err() itself; sshx.Run turns that back into
		// the ctx error so a caller can tell "timed out" from "the ssh
		// transport itself failed" and not retry something that was never
		// going to finish.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return res, fmt.Errorf("%s: %w", name, err)
		}
		// ssh could not be started at all (not installed, not executable).
		// Nothing ran in the guest, so there is no exit status to report.
		return ExecResult{}, fmt.Errorf("%s: ssh: %w", name, err)
	}
	return res, nil
}

// shellJoin renders an argv as a single string that the guest's shell will
// parse back into exactly those words.
//
// Every element is wrapped in single quotes. POSIX shells treat every byte
// inside single quotes literally: no expansion, no globbing, no word
// splitting. A single quote cannot appear inside single quotes; shellJoin
// closes it, escapes it with a backslash, and reopens it: the standard
// shell idiom for a literal single quote inside a single-quoted string.
//
// This is deliberately not internal/installer's shellQuote. That function
// writes a value into a shell rc file for a host shell that may be fish,
// and this package must not import a front end (see doctor.go). Both
// exist for the same reason: an unquoted value is a value the shell
// rewrites.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
