package core

import "errors"

// ErrLockOutOfDate identifies a project declaration that is not pinned.
var ErrLockOutOfDate = errors.New("stoat.lock is out of date")

// The remote-recipes implementation replaces these compile-only stubs.
func SyncRecipes() error { return nil }

func RecipeUsers(string) ([]string, error) { return nil, nil }
