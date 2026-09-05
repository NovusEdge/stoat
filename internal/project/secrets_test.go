package project

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSecrets(t *testing.T, dir, body string, mode os.FileMode) {
	t.Helper()
	cache := filepath.Join(dir, CacheDir)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "secrets.toml"), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestSecretsReadPerKey(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\n")
	writeSecrets(t, dir, "[dev.tailscale]\nauthkey = \"tskey-abc\"\n", 0o600)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Secrets("dev")
	if err != nil {
		t.Fatal(err)
	}
	if got["tailscale"]["authkey"] != "tskey-abc" {
		t.Errorf("secrets = %v, want tailscale.authkey", got)
	}
}

func TestSecretsAbsentIsNotAnError(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\n")
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Secrets("dev")
	if err != nil || len(got) != 0 {
		t.Errorf("Secrets = %v,%v, want an empty map and no error", got, err)
	}
}

func TestSecretsWideModeIsRefused(t *testing.T) {
	dir := write(t, "schema = 1\n\n[vms.dev]\nimage = \"a\"\n")
	writeSecrets(t, dir, "[dev.tailscale]\nauthkey = \"x\"\n", 0o644)
	p, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Secrets("dev")
	if err == nil {
		t.Fatal("Secrets read a world-readable file")
	}
	want := "secrets.toml: mode 0644, want 0600"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}
