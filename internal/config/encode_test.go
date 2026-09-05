package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The comment is the contract: a human editing vm.toml must be able to see
// which tables stoat rewrites on every apply.
func TestSaveCommentsTheAppliedTable(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	v := &VM{
		Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200,
		Recipes: []string{"docker"},
		Applied: map[string]AppliedRecipe{
			"docker": {Version: "1.2.0", Hash: "sha256:abc", At: time.Unix(0, 0).UTC()},
		},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(Root(), "work", "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "[applied") {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if strings.TrimSpace(lines[j]) == "" {
				continue
			}
			if !strings.Contains(lines[j], "written by stoat; do not edit") {
				t.Fatalf("line before %q is %q, want the stoat-owned comment", l, lines[j])
			}
			return
		}
		t.Fatalf("nothing precedes %q", l)
	}
	t.Fatalf("no applied table in:\n%s", b)
}

// An absent allow_exec key means true. A vm.toml written before the field
// existed must not lose exec.
func TestLoadDefaultsAllowExecTrue(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"old\"\nmode = \"live\"\nram = 1024\ncpus = 1\nsshport = 2201\n"
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Load("old")
	if err != nil {
		t.Fatal(err)
	}
	if !v.AllowExec {
		t.Error("AllowExec = false, want true for an absent key")
	}
}

func TestLoadKeepsExplicitAllowExecFalse(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "locked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"locked\"\nmode = \"live\"\nram = 1024\ncpus = 1\nsshport = 2202\nallow_exec = false\n"
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Load("locked")
	if err != nil {
		t.Fatal(err)
	}
	if v.AllowExec {
		t.Error("AllowExec = true, want the explicit false")
	}
}
