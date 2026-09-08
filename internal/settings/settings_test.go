package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReturnsZeroWhenAbsent(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for an absent file", err)
	}
	if s.Providers.GCE.Project != "" {
		t.Errorf("Project = %q, want empty", s.Providers.GCE.Project)
	}
}

func TestLoadReadsProviderTable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STOAT_HOME", dir)
	body := "[providers.gce]\nproject = \"p1\"\nzone = \"us-central1-a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if s.Providers.GCE.Project != "p1" {
		t.Errorf("Project = %q, want p1", s.Providers.GCE.Project)
	}
	if s.Providers.GCE.Zone != "us-central1-a" {
		t.Errorf("Zone = %q, want us-central1-a", s.Providers.GCE.Zone)
	}
}

func TestLoadRejectsAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STOAT_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[providers.gce\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("Load() error = nil, want a parse error for malformed toml")
	}
}
