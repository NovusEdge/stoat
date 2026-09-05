package recipes

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/tomlx"
)

// LockSchema is the lock format this stoat writes and the highest it reads.
const LockSchema = 1

// lockHeader sits above the encoded lock. tomlx.Encode writes no comments, so
// SaveLock prepends this itself.
const lockHeader = "# stoat.lock: written by stoat; do not edit\n"

// Lock pins every remote recipe in one scope to a commit.
type Lock struct {
	Schema  int                  `toml:"schema"`
	Recipes map[string]LockEntry `toml:"recipes"`
}

// LockEntry is one recipe's pin. Ref is the tag or branch the user asked for.
// Commit is the full sha that ref resolved to when the lock was written.
type LockEntry struct {
	Source string `toml:"source"`
	Ref    string `toml:"ref"`
	Commit string `toml:"commit"`
	Added  string `toml:"added"`
}

// LoadLock reads path. A missing file is an empty lock, not an error: the
// first recipe add in a scope creates it.
func LoadLock(path string) (Lock, error) {
	l := Lock{Schema: LockSchema, Recipes: map[string]LockEntry{}}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return l, nil
		}
		return Lock{}, err
	}
	if err := tomlx.Decode(path, &l, tomlx.Reject); err != nil {
		return Lock{}, err
	}
	if l.Schema > LockSchema {
		return Lock{}, fmt.Errorf("%s: schema %d is newer than this stoat (%d)", filepath.Base(path), l.Schema, LockSchema)
	}
	if l.Recipes == nil {
		l.Recipes = map[string]LockEntry{}
	}
	return l, nil
}

// SaveLock writes l with the do-not-edit header above it. The completed file
// is renamed into place only after encoding succeeds.
func SaveLock(path string, l Lock) (err error) {
	l.Schema = LockSchema
	if l.Recipes == nil {
		l.Recipes = map[string]LockEntry{}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode, err := existingFileMode(path)
	if err != nil {
		return err
	}
	stageDir, err := os.MkdirTemp(dir, ".stoat-lock-*")
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(stageDir, "lock")
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.RemoveAll(stageDir)
		return err
	}
	defer func() {
		if removeErr := os.RemoveAll(stageDir); removeErr != nil && err == nil {
			err = removeErr
		}
	}()

	if err := tomlx.Encode(tmpPath, l); err != nil {
		return err
	}
	body, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, append([]byte(lockHeader), body...), 0o644); err != nil {
		return err
	}
	if mode != 0 {
		if err := os.Chmod(tmpPath, mode); err != nil {
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func existingFileMode(path string) (os.FileMode, error) {
	info, err := os.Stat(path)
	if err == nil {
		return info.Mode().Perm(), nil
	}
	if os.IsNotExist(err) {
		return 0, nil
	}
	return 0, err
}
