package installer

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Version is the same string the justfile stamps: the git description, or
// "dev" when there is no git to ask. It never fails, since a missing version
// must not stop an install.
func Version(repoDir string) string {
	cmd := exec.Command("git", "describe", "--tags", "--always")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "dev"
	}
	if v := strings.TrimSpace(string(out)); v != "" {
		return v
	}
	return "dev"
}

// Build compiles ./cmd/stoat into outPath with the same ldflags as
// `just build`, so a binary from the installer is identical to one from make.
//
// Any go build failure is returned with its stderr attached: "exit status 1"
// on its own tells the user nothing they can act on.
func Build(repoDir, version, outPath string) error {
	cmd := exec.Command("go", "build",
		"-ldflags", "-s -w -X main.version="+version,
		"-o", outPath,
		"./cmd/stoat",
	)
	cmd.Dir = repoDir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return &BuildError{Err: err, Output: msg}
		}
		return &BuildError{Err: err}
	}
	return nil
}

// BuildError carries go build's stderr so the installer can show it.
type BuildError struct {
	Err    error
	Output string
}

func (e *BuildError) Error() string {
	if e.Output == "" {
		return "go build failed: " + e.Err.Error()
	}
	return "go build failed: " + e.Output
}

func (e *BuildError) Unwrap() error { return e.Err }

// Install copies srcPath into destDir as an executable, creating destDir if
// needed.
//
// It writes to a temp file and renames, rather than opening the destination
// for write. Replacing a running binary that way is atomic and avoids
// ETXTBSY, the error a reinstall over a running stoat would otherwise hit.
// InstallData creates ~/.stoat subdirectories and copies bundled recipes from
// the repo. Safe to call on reinstall: existing user files are not overwritten.
func InstallData(repoDir, home string) error {
	root := filepath.Join(home, ".stoat")
	for _, d := range []string{"recipes", "isos", "logs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			return err
		}
	}
	// Copy bundled recipes from repo, skipping any the user already has.
	srcDir := filepath.Join(repoDir, "internal", "recipes")
	destDir := filepath.Join(root, "recipes")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".sh") {
			continue
		}
		dst := filepath.Join(destDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue // ponytail: don't clobber user edits
		}
		if err := copyFile(filepath.Join(srcDir, name), dst); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Install copies srcPath into destDir as an executable, creating destDir if
// needed.
func Install(srcPath, destDir string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	destPath := filepath.Join(destDir, "stoat")

	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	tmp, err := os.CreateTemp(destDir, ".stoat-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return "", err
	}
	return destPath, nil
}
