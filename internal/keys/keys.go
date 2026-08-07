// Package keys manages stoat's SSH identities: the client key it connects
// with, and the host key baked into live VMs so it is stable across boots.
package keys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

func PrivatePath() string { return filepath.Join(config.Root(), "id_stoat") }
func publicPath() string  { return PrivatePath() + ".pub" }

// pairExists reports whether both halves of the keypair are present. A
// keypair is only usable as a unit; either half missing means broken.
//
// Only a genuine absence (fs.ErrNotExist) counts as missing. Any other stat
// error, such as a permissions problem on the directory, is treated as
// "present" rather than triggering repair. Regenerating the identity would
// invalidate the authorized_keys already baked into every running guest.
func pairExists(path string) bool {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if _, err := os.Stat(path + ".pub"); errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return true
}

// generate shells out to ssh-keygen, already a runtime dependency, rather
// than pulling in golang.org/x/crypto for a single call.
func generate(path, comment string) error {
	if pairExists(path) {
		return nil
	}
	// Serialised across processes, not just goroutines, using LockKeys.
	// LockKeys is a different lock file from config.Lock: Clone holds the
	// data-root lock and then calls Ensure, and flock does not nest, so
	// sharing one lock here deadlocked the whole suite against itself. See
	// Lock's doc comment.
	//
	// Every stoat command calls Ensure at startup. Without this lock, two
	// invocations against a fresh data root both see no key, both clear the
	// pair, and both run ssh-keygen against the same path. The loser then
	// finds the file recreated under it and ssh-keygen sits on an
	// interactive "Overwrite (y/n)?" prompt. Observed for real: six
	// concurrent `stoat create` calls, one failed exactly that way, on a
	// command that never asks the user anything.
	unlock, err := config.LockKeys()
	if err != nil {
		return err
	}
	defer unlock()
	// Re-checked under the lock. The winner has created the pair by the time
	// the loser gets here. Regenerating would hand out a key the winner's
	// already-seeded guest does not accept.
	if pairExists(path) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// ssh-keygen refuses to overwrite. A stale half of the pair, either the
	// private key or its .pub, would wedge this, so clear both first.
	os.Remove(path)
	os.Remove(path + ".pub")
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", path)
	// Extra safety alongside the lock above. ssh-keygen prompts on stdin
	// when it finds a file it did not expect, and a background or
	// MCP-driven stoat has no one to answer. With no stdin it fails and
	// reports immediately, instead of hanging for a keystroke that cannot
	// arrive.
	cmd.Stdin = nil
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ssh-keygen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Chmod(path, 0o600)
}

// Ensure creates stoat's client keypair if it does not exist.
func Ensure() error { return generate(PrivatePath(), "stoat") }

// PublicKey returns the trimmed client public key.
func PublicKey() (string, error) {
	b, err := os.ReadFile(publicPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// GuestHostKey returns the host keypair baked into live VMs. Stable across
// boots so ssh does not report a changed host key on every start.
func GuestHostKey() (priv, pub string, err error) {
	path := filepath.Join(config.Root(), "guest_host_ed25519_key")
	if err = generate(path, "stoat-guest"); err != nil {
		return "", "", err
	}
	p, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	q, err := os.ReadFile(path + ".pub")
	if err != nil {
		return "", "", err
	}
	return string(p), string(q), nil
}
