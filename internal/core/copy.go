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

// CopyTo copies localPath, a path on the HOST, to remotePath inside VM
// name's guest. CopyFrom is the mirror: remotePath on the guest to localPath
// on the host.
//
// Both exist for the same reason Exec does (see exec.go): getting a file in
// or out of a guest today means the user sets up a 9p share first, which is
// setup an agent driving stoat should never have to ask a human to go do.
// scp over the already-forwarded ssh port needs nothing extra.
//
// ctx cancels the scp process, for the same reason it exists on Exec: a
// large copy over a slow link is exactly the kind of local work that has no
// other way to stop.
//
// REMOTE-PATH QUOTING: remotePath is placed into scp's argv completely
// unquoted, on purpose — not an oversight carried over from Exec's
// shellJoin. Exec sends its command through the GUEST'S shell (ssh
// concatenates trailing args and hands them to a remote `sh -c`), so it MUST
// quote to survive that re-parse. scp is different: since OpenSSH 9.0 (the
// version on every host this was checked against — `ssh -V` here reports
// 10.4p1) it uses the SFTP protocol by default, not the legacy SCP protocol,
// and `man scp`'s CAVEATS section says explicitly that careful shell quoting
// is a concern only for the legacy protocol (`-O`). Under SFTP there is no
// remote shell in the path at all — scp parses "user@host:path" locally and
// hands path to the SFTP subsystem as a literal byte string. A remote path
// containing a space therefore needs NO escaping and MUST NOT be given any:
// wrapping it in quotes the way shellJoin does would create a file on the
// guest literally named with quote characters in it, which is a worse bug
// than the one that quoting would be trying to prevent. See
// sshx.CopyArgs and TestCopyArgsRemotePathIsNotShellQuoted.
//
// BOOT-VERIFIED 2026-08-03 on real Alpine 3.24.1 cloud, both directions,
// including `cptest:/tmp/my file.txt` arriving as ONE file with a space in it.
// Had the remote path been shell-quoted the way Exec quotes its argv, the
// guest would have got a file literally named with quote characters — so Exec
// and this function genuinely need OPPOSITE treatment, and both halves of that
// are now checked against the same guest rather than reasoned about.
//
// The SFTP default does carry one dependency worth knowing: the guest must
// provide an sftp subsystem, and OpenSSH 9+ does NOT silently fall back to the
// legacy protocol when it is missing — scp fails outright. Alpine's cloud
// image ships /usr/lib/ssh/sftp-server with `Subsystem sftp internal-sftp`
// (verified on the same boot), so this is fine for the images stoat ships.
// A stripped BYO guest without it will fail with scp's own error rather than
// silently doing something else, which is the acceptable failure mode.
//
// core does NOT sandbox localPath to any particular host directory (design
// §8 decision 1, §10.3): like Exec, this is a library call that the TUI and
// CLI already let a user point anywhere, and gating what host paths may be
// read or written belongs at the MCP boundary an agent actually crosses, not
// here.
func CopyTo(ctx context.Context, name, localPath, remotePath string) error {
	return doCopy(ctx, name, localPath, remotePath, true)
}

// CopyFrom is CopyTo's mirror — see its doc comment for the shared design
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
	// Checked before dialling, exactly as Exec does and for the same reason:
	// a stopped VM is the overwhelmingly common cause of an scp failure here,
	// and ErrNotRunning is a far more useful answer than scp's own "connection
	// refused" surfacing through a bare non-zero exit.
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
	// *exec.ExitError rather than ctx.Err() — see Exec's identical handling
	// for why that distinction is worth preserving: a caller that cannot tell
	// "timed out" from "scp itself failed" will retry something that was
	// never going to finish.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", name, ctxErr)
	}

	var ee *exec.ExitError
	if errors.As(err, &ee) && stderr.Len() > 0 {
		// scp ran and failed — its own stderr (no such file, permission
		// denied, ...) is a far better error than a bare exit status.
		return fmt.Errorf("%s: scp: %s", name, strings.TrimSpace(stderr.String()))
	}
	// scp could not be started at all (not installed, not executable).
	return fmt.Errorf("%s: scp: %w", name, err)
}
