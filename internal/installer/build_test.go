package installer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Install must produce an executable file, creating the destination directory
// if it is not there, since ~/.local/bin often does not exist yet.
func TestInstallCopiesAndCreatesDir(t *testing.T) {
	src := filepath.Join(t.TempDir(), "stoat")
	content := []byte("#!/bin/sh\necho hi\n")
	if err := os.WriteFile(src, content, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "nested", "bin")

	got, err := Install(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dest, "stoat"); got != want {
		t.Errorf("installed path = %q, want %q", got, want)
	}

	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed file is not executable: mode %v", info.Mode())
	}
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(content) {
		t.Errorf("content = %q, want %q", b, content)
	}
}

// Reinstalling over an existing binary must work: that is the common case,
// and on Linux writing over a running executable needs the file replaced, not
// opened for write.
func TestInstallOverwrites(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "stoat"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(t.TempDir(), "stoat")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Install(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Errorf("content = %q, want %q", b, "new")
	}
}

// Version must never fail the install; a tarball with no .git still installs.
func TestVersionFallsBackToDev(t *testing.T) {
	if got := Version(t.TempDir()); got != "dev" {
		t.Errorf("Version outside a git repo = %q, want %q", got, "dev")
	}
}

// The real thing: building stoat from this repo must succeed and produce a
// binary that runs and reports the version we stamped.
func TestBuildProducesRunnableBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("go build is slow; skipped under -short")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "stoat")

	if err := Build(repo, "v0.0.0-test", out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("built file is not executable: mode %v", info.Mode())
	}

	got, err := exec.Command(out, "--version").Output()
	if err != nil {
		t.Fatalf("built binary would not run: %v", err)
	}
	if !strings.Contains(string(got), "v0.0.0-test") {
		t.Errorf("version not stamped: got %q", got)
	}
}

// A build failure must surface the compiler's actual output, not just
// "exit status 1", since that is the only thing a user could act on.
func TestBuildFailureSurfacesCompilerOutput(t *testing.T) {
	repo := t.TempDir()
	cmdDir := filepath.Join(repo, "cmd", "stoat")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "package main\n\nfunc main() {\n\tthis is not valid go\n}\n"
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module broken\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "stoat")
	err := Build(repo, "v0.0.0-test", out)
	if err == nil {
		t.Fatal("expected an error building broken source, got nil")
	}

	var buildErr *BuildError
	if !errors.As(err, &buildErr) {
		t.Fatalf("error is not a *BuildError: %v (%T)", err, err)
	}
	if buildErr.Output == "" {
		t.Error("BuildError.Output is empty; compiler output was not captured")
	}
	if !strings.Contains(buildErr.Error(), "syntax error") && !strings.Contains(buildErr.Error(), "expected") {
		t.Errorf("BuildError.Error() does not look like a compiler message: %q", buildErr.Error())
	}
}
