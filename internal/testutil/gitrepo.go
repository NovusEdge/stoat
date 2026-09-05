package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitRepo creates a bare repository with one commit on branch "main" and
// returns its path. A caller passes that path to git as a URL, so no test
// needs the network.
func GitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "src.git")
	runGit(t, "", "git", "init", "--bare", "-b", "main", bare)
	GitCommit(t, bare, files, "")
	return bare
}

// GitCommit clones bare, writes files, commits, optionally tags, and pushes.
// It returns the new commit sha.
func GitCommit(t *testing.T, bare string, files map[string]string, tag string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "git", "clone", "-q", bare, work)
	runGit(t, work, "git", "config", "user.email", "test@example.com")
	runGit(t, work, "git", "config", "user.name", "test")
	for name, body := range files {
		WriteFile(t, filepath.Join(work, name), body)
	}
	runGit(t, work, "git", "add", "-A")
	runGit(t, work, "git", "commit", "-q", "-m", "commit")
	if tag != "" {
		runGit(t, work, "git", "tag", tag)
		runGit(t, work, "git", "push", "-q", "origin", tag)
	}
	runGit(t, work, "git", "push", "-q", "origin", "HEAD:main")
	out, err := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// GitClone creates a work tree from bare for tests that exercise a work-tree
// operation without making the operation under test perform the clone.
func GitClone(t *testing.T, bare string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	runGit(t, "", "git", "clone", "-q", bare, work)
	return work
}

// WriteFile writes body to path, creating parent directories.
func WriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
