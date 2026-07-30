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

func TestEnsureRepairsMissingPublicHalf(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(PrivatePath() + ".pub"); err != nil {
		t.Fatal(err)
	}

	// Repairing a broken pair regenerates both halves; this only proves
	// the pair converges on something usable, not that a healthy pair
	// survives untouched (see TestEnsureDoesNotRegenerateHealthyPair).
	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := PublicKey(); err != nil {
		t.Fatalf("PublicKey failed after repair: %v", err)
	}
}

func TestEnsureRepairsMissingPrivateHalf(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(PrivatePath()); err != nil {
		t.Fatal(err)
	}

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(PrivatePath()); err != nil {
		t.Fatalf("private key missing after repair: %v", err)
	}
	if _, err := PublicKey(); err != nil {
		t.Fatalf("PublicKey failed after repair: %v", err)
	}
}

func TestEnsureDoesNotRegenerateHealthyPair(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	privBefore, _ := os.ReadFile(PrivatePath())
	pubBefore, _ := os.ReadFile(PrivatePath() + ".pub")

	if err := Ensure(); err != nil {
		t.Fatal(err)
	}
	privAfter, _ := os.ReadFile(PrivatePath())
	pubAfter, _ := os.ReadFile(PrivatePath() + ".pub")

	if string(privBefore) != string(privAfter) {
		t.Error("Ensure regenerated a healthy private key")
	}
	if string(pubBefore) != string(pubAfter) {
		t.Error("Ensure regenerated a healthy public key")
	}
}

func TestSSHKeygenMissingErrorNamesCause(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // empty dir: ssh-keygen cannot be found

	err := Ensure()
	if err == nil {
		t.Fatal("expected error when ssh-keygen is not on PATH")
	}
	if !strings.Contains(err.Error(), "executable file not found") &&
		!strings.Contains(strings.ToLower(err.Error()), "ssh-keygen") {
		t.Errorf("error does not name the cause: %v", err)
	}
	// Specifically, exec's error text (not just an empty CombinedOutput)
	// must appear.
	if strings.HasSuffix(strings.TrimSpace(err.Error()), "ssh-keygen:") {
		t.Errorf("error carries no information beyond the prefix: %v", err)
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
