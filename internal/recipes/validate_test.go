package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTreeAcceptsSchema3ScriptsAndResolvedSymlinkChain(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "recipe.toml"), `schema = 3
name = "demo"
script = "install.sh"

[scripts]
alpine = "scripts/alpine.sh"

[params.user]
type = "string"
default = "dev"
`)
	writeFile(t, filepath.Join(dir, "install.sh"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(dir, "common.sh"), "#!/bin/sh\necho common\n")
	if err := os.MkdirAll(filepath.Join(dir, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../common.sh", filepath.Join(dir, "links", "first.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../links/first.sh", filepath.Join(dir, "scripts", "alpine.sh")); err != nil {
		t.Fatal(err)
	}

	if err := ValidateTree(dir, "demo"); err != nil {
		t.Fatalf("ValidateTree() = %v, want nil", err)
	}
}

func TestValidateTreeRejectsASymlinkChainThatEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "recipe.toml"), `schema = 3
name = "demo"
script = "install.sh"

[scripts]
alpine = "scripts/alpine.sh"
`)
	writeFile(t, filepath.Join(dir, "install.sh"), "#!/bin/sh\n")
	if err := os.MkdirAll(filepath.Join(dir, "links", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "links", "nested", "outside.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../links/nested/outside.sh", filepath.Join(dir, "scripts", "alpine.sh")); err != nil {
		t.Fatal(err)
	}

	err := ValidateTree(dir, "demo")
	if err == nil || !strings.Contains(err.Error(), "points outside the recipe") {
		t.Fatalf("ValidateTree() = %v, want an outside-tree error", err)
	}
}

func TestValidateTreeRequiresAUniqueStrictRootManifest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing", want: "no recipe.toml at the repository root"},
		{name: "unknown field", body: "name = \"demo\"\nscript = \"install.sh\"\nunexpected = true\n", want: "unexpected"},
		{name: "missing OS override", body: "schema = 3\nname = \"demo\"\nscript = \"install.sh\"\n[scripts]\nalpine = \"missing.sh\"\n", want: "missing.sh"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.body != "" {
				writeFile(t, filepath.Join(dir, "recipe.toml"), tc.body)
				writeFile(t, filepath.Join(dir, "install.sh"), "#!/bin/sh\n")
			}
			err := ValidateTree(dir, "demo")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateTree() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateTreeRejectsANameMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "recipe.toml"), "name = \"other\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(dir, "install.sh"), "#!/bin/sh\n")

	err := ValidateTree(dir, "demo")
	if err == nil || !strings.Contains(err.Error(), `recipe.toml is named "other"`) {
		t.Fatalf("ValidateTree() = %v, want name mismatch", err)
	}
}

func TestCheckCollisionDistinguishesSameScopeAndShadowing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	wd := t.TempDir()
	t.Chdir(wd)

	globalCache := filepath.Join(home, "recipes", "demo")
	writeFile(t, filepath.Join(globalCache, "recipe.toml"), "name = \"demo\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(globalCache, "install.sh"), "#!/bin/sh\n")
	if err := SaveLock(filepath.Join(home, "stoat.lock"), Lock{Recipes: map[string]LockEntry{
		"demo": {Source: "local", Ref: "main", Commit: strings.Repeat("a", 40)},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := CheckCollision("demo", "global"); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("same-scope collision = %v, want a collision", err)
	}
	if err := CheckCollision("demo", "project"); err != nil {
		t.Fatalf("project shadowing global = %v, want nil", err)
	}

	local := filepath.Join(home, "recipes", "handmade")
	writeFile(t, filepath.Join(local, "recipe.toml"), "name = \"handmade\"\nscript = \"install.sh\"\n")
	writeFile(t, filepath.Join(local, "install.sh"), "#!/bin/sh\n")
	if err := CheckCollision("handmade", "global"); err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("local collision = %v, want a local collision", err)
	}

	if err := Install(); err != nil {
		t.Fatal(err)
	}
	if err := CheckCollision("docker", "global"); err == nil || !strings.Contains(err.Error(), "bundled") {
		t.Fatalf("bundled collision = %v, want a bundled collision", err)
	}

}
