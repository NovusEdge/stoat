// Package gitx is the test-authoring stub for the remote-recipes git boundary.
// The implementation lands in the following role's task commit.
package gitx

import "errors"

var (
	ErrNoGit = errors.New("git is not installed")
	ErrNoRef = errors.New("no such tag or branch")
)

func Available() bool { return false }

func Clone(_, _, _ string) error { return nil }

func CloneFull(_, _ string) error { return nil }

func Checkout(_, _ string) error { return nil }

func Fetch(_, _ string) error { return nil }

func RevParse(_, _ string) (string, error) { return "", nil }

func Dirty(_ string) (bool, error) { return false, nil }
