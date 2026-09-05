package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSecretsRoundTripAt0600(t *testing.T) {
	dir := t.TempDir()
	if got, want := (&VM{Dir: dir}).SecretsPath(), filepath.Join(dir, SecretsName); got != want {
		t.Errorf("SecretsPath = %q, want %q", got, want)
	}
	s := Secrets{"docker": {"zkey": "z", "authkey": "tskey-abc", "unset": ""}}
	if err := SaveSecrets(dir, s); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, SecretsName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", fi.Mode().Perm())
	}
	got, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["docker"]["authkey"] != "tskey-abc" {
		t.Errorf("got %v", got)
	}
	if names := got.Names("docker"); !slices.Equal(names, []string{"authkey", "zkey"}) {
		t.Errorf("Names = %v", names)
	}
}

func TestSaveSecretsProtectsExistingWideFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SecretsName)
	if err := os.WriteFile(path, []byte("docker.authkey = \"old-secret\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecrets(dir, Secrets{"docker": {"authkey": "new-secret"}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600 after replacing an existing file", fi.Mode().Perm())
	}
	got, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["docker"]["authkey"] != "new-secret" {
		t.Errorf("got %v, want the replacement secret", got)
	}
}

func TestLoadSecretsRefusesWideModeWithoutSecretValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SecretsName)
	sentinel := "tskey-secret-sentinel"
	if err := os.WriteFile(path, []byte("docker.authkey = \""+sentinel+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSecrets(dir)
	if err == nil || !strings.Contains(err.Error(), "secrets.toml: mode 0644, want 0600") {
		t.Fatalf("err = %v, want the mode refusal", err)
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("error exposes the secret value: %v", err)
	}
}

func TestLoadSecretsMissingFileIsEmpty(t *testing.T) {
	got, err := LoadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no secrets", got)
	}
}

// The spec writes "docker.authkey"; TOML reads it as a nested table. Both
// spellings must load the same.
func TestLoadSecretsAcceptsDottedAndTableForms(t *testing.T) {
	for _, body := range []string{
		"docker.authkey = \"x\"\n",
		"[docker]\nauthkey = \"x\"\n",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, SecretsName), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadSecrets(dir)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if got["docker"]["authkey"] != "x" {
			t.Errorf("%q: got %v", body, got)
		}
	}
}
