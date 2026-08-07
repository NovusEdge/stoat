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

// CopyTo copies localPath on the host to remotePath in VM name's guest over
// scp on the forwarded ssh port. ctx cancels the scp process.
//
// remotePath goes into scp's argv unquoted, the opposite of Exec's shellJoin.
// Exec runs its command through the guest shell and must quote to survive the
// re-parse. scp since OpenSSH 9.0 uses SFTP by default: it parses
// "user@host:path" locally and hands the path to the SFTP subsystem as literal
// bytes, no remote shell. Quoting remotePath would create a file named with
// literal quote characters. See sshx.CopyArgs and
// TestCopyArgsRemotePathIsNotShellQuoted.
//
// SFTP needs the guest to run an sftp subsystem; OpenSSH 9+ fails outright
// rather than falling back to legacy scp. Alpine's cloud image ships it.
//
// core does not sandbox localPath (design §8 decision 1): path gating belongs
// at the MCP boundary, not in a library call the TUI and CLI already leave open.
func CopyTo(ctx context.Context, name, localPath, remotePath string) error {
	return doCopy(ctx, name, localPath, remotePath, true)
}

// CopyFrom is CopyTo's mirror; see its doc comment for the shared design
// (ctx, quoting, sandboxing).
func CopyFrom(ctx context.Context, name, remotePath, localPath string) error {
	return doCopy(ctx, name, localPath, remotePath, false)
}

// doCopy is CopyTo/CopyFrom's shared body. toRemote picks the direction the
// same way sshx.CopyArgs does: true means localPath is the source and the
// guest is the destination.
func doCopy(ctx context.Context, name, localPath, remotePath string, toRemote bool) error {
	if strings.TrimSpace(localPath) == "" || strings.TrimSpace(remotePath) == "" {
		return fmt.Errorf("%w: local and remote path are both required", ErrInvalidSpec)
	}
	v, err := load(name)
	if err != nil {
		return err
	}
	// A stopped VM is the common cause of an scp failure. Report it as
	// ErrNotRunning rather than let scp's "connection refused" surface as a
	// bare non-zero exit. Exec checks the same way.
	if !qemu.Running(v) {
		return fmt.Errorf("%w: %s", ErrNotRunning, name)
	}

	var stderr bytes.Buffer
	c := exec.CommandContext(ctx, "scp", sshx.CopyArgs(v, localPath, remotePath, toRemote)...)
	c.Stderr = &stderr

	err = c.Run()
	if err == nil {
		return nil
	}

	// ctx expiring kills the scp process, which surfaces as a generic
	// *exec.ExitError rather than ctx.Err(); see Exec's identical handling
	// for why that distinction is worth preserving: a caller that cannot tell
	// "timed out" from "scp itself failed" will retry something that was
	// never going to finish.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", name, ctxErr)
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) && stderr.Len() > 0 {
		// scp ran and failed: its own stderr (no such file, permission
		// denied, ...) is a far better error than a bare exit status.
		return fmt.Errorf("%s: scp: %s", name, strings.TrimSpace(stderr.String()))
	}
	// scp could not be started at all (not installed, not executable).
	return fmt.Errorf("%s: scp: %w", name, err)
}
