package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGcloud(t *testing.T, project, zone string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLOUDSDK_CONFIG", dir)
	if err := os.MkdirAll(filepath.Join(dir, "configurations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active_config"), []byte("default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "[core]\nproject = " + project + "\n[compute]\nzone = " + zone + "\n"
	if err := os.WriteFile(filepath.Join(dir, "configurations", "config_default"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePrefersTheFlag(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeGcloud(t, "from-gcloud", "us-central1-a")
	got, err := ResolveGCE("from-flag", "europe-west4-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "from-flag" || got.Source != SourceFlag {
		t.Errorf("Resolved = %+v, want from-flag via the flag", got)
	}
}

func TestResolveFallsBackToGcloud(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeGcloud(t, "from-gcloud", "us-central1-a")
	got, err := ResolveGCE("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "from-gcloud" || got.Zone != "us-central1-a" {
		t.Errorf("Resolved = %+v, want gcloud's values", got)
	}
	if got.Source != SourceGcloud {
		t.Errorf("Source = %q; create output must be able to say the values came from gcloud", got.Source)
	}
}

func TestResolveNamesWhatIsMissing(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	t.Setenv("CLOUDSDK_CONFIG", t.TempDir())
	_, err := ResolveGCE("", "")
	if err == nil {
		t.Fatal("ResolveGCE() = nil error with nothing configured")
	}
	for _, want := range []string{"--project", "providers.gce", "gcloud"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
