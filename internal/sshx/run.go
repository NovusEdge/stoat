package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

// Quote renders argv as one string the guest shell parses back into exactly
// those words. ssh concatenates its trailing arguments with spaces and hands
// the result to the remote shell, so an unquoted argv loses every word
// boundary: Run(["touch", "my file"]) would create two files.
//
// Every element is wrapped in single quotes, inside which a POSIX shell
// treats every byte literally. A single quote cannot appear inside single
// quotes, so it is closed, escaped, and reopened.
func Quote(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// ErrUnreachable marks an ssh failure before any remote command ran: the
// guest refused, dropped, or never answered the connection. Right after a
// start, sshd is not up yet and every call fails this way.
var ErrUnreachable = errors.New("ssh cannot reach the guest")

// transportMarkers are the fragments ssh prints, at LogLevel=ERROR, when the
// connection fails before a session opens.
var transportMarkers = []string{
	"ssh: connect to host",
	"ssh: Could not resolve hostname",
	"banner exchange",
	"kex_exchange_identification",
	"Connection closed by",
	"Connection reset by",
	"Connection timed out",
}

// IsTransportFailure reports whether stderr is ssh's or scp's own report of a
// connection that never opened. Exit 255 alone is not enough: a remote command
// can exit 255 too, so the text decides.
func IsTransportFailure(stderr []byte) bool {
	s := string(stderr)
	for _, m := range transportMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// NewUnreachable wraps ErrUnreachable with the VM name and ssh's first line.
func NewUnreachable(name string, stderr []byte) error {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stderr)), "\n")
	return fmt.Errorf("%s: %w: %s", name, ErrUnreachable, line)
}

// Run executes argv inside v's guest and returns the guest's raw output and
// exit status. A command that ran and exited non-zero is a result, not an
// error; Run returns an error only when ssh could not run at all, or when it
// could not connect (ErrUnreachable).
//
// argv is an argv, never a shell string. Run is the one place stoat's in-VM
// tools quote it for the guest shell, so no tool caller has to decide.
//
// root applies the guest's escalate prefix for a non-root ssh user, through
// the same escalate helper Provision and RunCheck already use. A tool never
// escalates on its own; the caller passes root only for a call whose
// contract says so.
func Run(ctx context.Context, v *config.VM, root bool, argv []string, stdin io.Reader) ([]byte, []byte, int, error) {
	remote := argv
	if root {
		remote = escalate(v, argv)
	}
	ep, err := endpointFor(v)
	if err != nil {
		return nil, nil, 0, err
	}
	var out, errb bytes.Buffer
	c := exec.CommandContext(ctx, "ssh", Args(ep, Quote(remote))...)
	c.Stdin = stdin
	c.Stdout = &out
	c.Stderr = &errb

	err = c.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), errb.Bytes(), 0, nil
	case errors.As(err, &ee):
		if ctxErr := ctx.Err(); ctxErr != nil {
			// ctx expiring kills the process and surfaces as an ExitError.
			// A caller that cannot tell "timed out" from "the command
			// failed" retries something that was never going to finish.
			return out.Bytes(), errb.Bytes(), ee.ExitCode(), ctxErr
		}
		if ee.ExitCode() == 255 && IsTransportFailure(errb.Bytes()) {
			return out.Bytes(), errb.Bytes(), 255, NewUnreachable(v.Name, errb.Bytes())
		}
		return out.Bytes(), errb.Bytes(), ee.ExitCode(), nil
	default:
		return nil, nil, 0, err
	}
}
