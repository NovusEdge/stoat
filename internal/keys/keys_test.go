package keys

import (
	"os"
	"strings"
	"testing"
)

func TestEnsureIsIdempotentAndProducesUsableKeys(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	pub, err := PublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("public key does not look like ed25519: %q", pub)
	}
	if strings.Contains(pub, "\n") {
		t.Error("public key must be a single trimmed line")
	}

	fi, err := os.Stat(PrivatePath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %v, want 0600", fi.Mode().Perm())
	}

	// Idempotent: a second Ensure must not replace the key.
	before, _ := os.ReadFile(PrivatePath())
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(PrivatePath())
	if string(before) != string(after) {
		t.Error("Ensure regenerated an existing key")
	}
}

func TestGuestHostKeyStable(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	priv1, pub1, err := GuestHostKey()
	if err != nil {
		t.Fatal(err)
	}
	priv2, pub2, err := GuestHostKey()
	if err != nil {
		t.Fatal(err)
	}
	if priv1 != priv2 || pub1 != pub2 {
		t.Error("guest host key changed between calls")
	}
	if !strings.HasPrefix(pub1, "ssh-ed25519 ") {
		t.Errorf("guest host pubkey malformed: %q", pub1)
	}
}
