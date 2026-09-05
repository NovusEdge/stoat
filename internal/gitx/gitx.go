// Package gitx runs git as a subprocess. Every operation has one fixed argv,
// so the command a user sees in an error is the command stoat ran.
package gitx

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var (
	ErrNoGit = errors.New("git is not installed")
	ErrNoRef = errors.New("no such tag or branch")
)

// Available reports whether git is on PATH.
func Available() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// Clone makes a shallow clone of url at ref into dst. An empty ref takes the
// remote's default branch.
func Clone(url, ref, dst string) error {
	args := []string{"clone", "--quiet", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dst)
	out, err := run("", args...)
	if err != nil && ref != "" && strings.Contains(strings.ToLower(out), "not found in upstream origin") {
		return fmt.Errorf("%w %q", ErrNoRef, ref)
	}
	return err
}

// CloneFull clones every commit of url into dst and checks out nothing. Sync
// uses it because a shallow fetch of an arbitrary commit needs a server
// setting that a plain git host does not have.
func CloneFull(url, dst string) error {
	_, err := run("", "clone", "--quiet", "--no-checkout", "--", url, dst)
	return err
}

// Checkout moves dir's work tree to rev and detaches HEAD.
func Checkout(dir, rev string) error {
	out, err := run(dir, "checkout", "--quiet", "--detach", rev)
	if err != nil && strings.Contains(strings.ToLower(out), "did not match any file") {
		return fmt.Errorf("%w %q", ErrNoRef, rev)
	}
	return err
}

// Fetch downloads ref from dir's origin and leaves it as FETCH_HEAD.
func Fetch(dir, ref string) error {
	args := []string{"fetch", "--quiet", "--depth", "1", "origin"}
	if ref != "" {
		args = append(args, ref)
	}
	out, err := run(dir, args...)
	if err != nil && ref != "" && strings.Contains(strings.ToLower(out), "couldn't find remote ref") {
		return fmt.Errorf("%w %q", ErrNoRef, ref)
	}
	return err
}

// RevParse returns the full sha rev resolves to in dir.
func RevParse(dir, rev string) (string, error) {
	out, err := run(dir, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Dirty reports whether dir's work tree has uncommitted changes, including
// untracked files.
func Dirty(dir string) (bool, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// run executes git in dir and returns its combined output. The output is
// returned on failure too, because the caller matches git's own message to
// tell a missing ref from a transport failure.
func run(dir string, args ...string) (string, error) {
	if !Available() {
		return "", ErrNoGit
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
