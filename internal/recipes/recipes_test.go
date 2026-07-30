package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCopiesBundledRecipesAndPreservesEdits(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	if err := Install(); err != nil {
		t.Fatal(err)
	}
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range names {
		if n == "xfce" {
			found = true
		}
		if strings.HasSuffix(n, ".sh") {
			t.Errorf("List must return names without .sh, got %q", n)
		}
	}
	if !found {
		t.Fatalf("xfce recipe not installed, got %v", names)
	}

	// A user edit must survive a later Install (i.e. an upgrade).
	edited := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(Path("xfce"), []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(); err != nil {
		t.Fatal(err)
	}
	got, err := Read("xfce")
	if err != nil {
		t.Fatal(err)
	}
	if got != edited {
		t.Error("Install overwrote a user-edited recipe")
	}
}

func TestBundledRecipeDoesNotAssumeNetworklessApk(t *testing.T) {
	// The recipe runs after boot over ssh, so it may install from the
	// network — but it must set up repositories first.
	b, err := os.ReadFile(filepath.Join("xfce.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "setup-apkrepos") {
		t.Error("xfce.sh installs packages without configuring repositories first")
	}
}
