package recipes

import "errors"

// ErrDirty identifies a cache checkout with uncommitted changes.
var ErrDirty = errors.New("local changes")

// The remote-recipes implementation replaces these compile-only stubs.
func ParseRef(string) (source, gitRef string, isURL bool) { return "", "", false }

func Add(Scope, string, bool) (LockEntry, error) {
	return LockEntry{}, errors.New("remote recipe add is not implemented")
}

func Preview(string, string) (Manifest, string, error) {
	return Manifest{}, "", errors.New("remote recipe preview is not implemented")
}

func LockAll(Scope) (Lock, error) { return Lock{}, nil }

func Sync(Scope) error { return nil }

func StaleLock(Scope) (string, bool, error) { return "", false, nil }

func Update(Scope, []string) ([]LockEntry, error) { return nil, nil }

func Remove(Scope, string) error { return nil }
