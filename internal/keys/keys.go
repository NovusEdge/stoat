// Package keys manages stoat's SSH identities: the client key it connects
// with, and the host key baked into live VMs so it is stable across boots.
package keys

import (
	"fmt"
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
func pairExists(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	if _, err := os.Stat(path + ".pub"); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// ssh-keygen refuses to overwrite; a stale half of the pair (either
	// the private key or its .pub) would wedge us, so clear both first.
	os.Remove(path)
	os.Remove(path + ".pub")
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", comment, "-f", path)
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
