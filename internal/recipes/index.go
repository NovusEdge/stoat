package recipes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/gitx"
	"github.com/novusedge/stoat/internal/tomlx"
)

// DefaultIndexURL is the curated index STOAT_INDEX overrides. Git accepts a
// filesystem path as a URL, which keeps tests local.
const DefaultIndexURL = "https://github.com/novusedge/stoat-recipes"

const indexStampName = ".fetched"
const indexSourceName = ".source"
const indexLockName = ".index.lock"
const indexSchema = 1
const indexMaxAge = 24 * time.Hour

type Index struct {
	Schema  int                   `toml:"schema"`
	Recipes map[string]IndexEntry `toml:"recipes"`
}

type IndexEntry struct {
	Name        string   `toml:"-"`
	Source      string   `toml:"source"`
	Description string   `toml:"description"`
	OS          []string `toml:"os"`
}

// IndexURL is the index repository: STOAT_INDEX, or the curated default.
func IndexURL() string {
	if u := os.Getenv("STOAT_INDEX"); u != "" {
		return u
	}
	return DefaultIndexURL
}

// IndexDir is the local clone of the index.
func IndexDir() string { return filepath.Join(config.Root(), "index") }

// RefreshIndex clones the index into a staging directory, validates it, then
// swaps it into place. A failed refresh leaves the last usable clone intact.
func RefreshIndex(force bool) error {
	unlock, err := lockIndex()
	if err != nil {
		return err
	}
	operationErr := refreshIndexLocked(force)
	unlockErr := unlock()
	if operationErr != nil {
		return operationErr
	}
	return unlockErr
}

func refreshIndexLocked(force bool) (err error) {
	root := config.Root()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	dir := IndexDir()
	stamp := filepath.Join(dir, indexStampName)
	if !force {
		st, statErr := os.Stat(stamp)
		if statErr == nil {
			if time.Since(st.ModTime()) < indexMaxAge && indexSource(dir) == IndexURL() {
				return nil
			}
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}

	stage, err := os.MkdirTemp(root, ".stoat-index-stage-*")
	if err != nil {
		return err
	}
	stageActive := true
	defer func() {
		if stageActive {
			if removeErr := os.RemoveAll(stage); removeErr != nil && err == nil {
				err = removeErr
			}
		}
	}()

	if err := gitx.Clone(IndexURL(), "", stage); err != nil {
		return err
	}
	if _, err := loadIndex(stage); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, indexStampName), nil, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, indexSourceName), []byte(IndexURL()), 0o644); err != nil {
		return err
	}
	if err := replaceIndex(stage, dir); err != nil {
		return err
	}
	stageActive = false
	return nil
}

func indexSource(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, indexSourceName))
	if err != nil {
		return ""
	}
	return string(b)
}

func lockIndex() (func() error, error) {
	root := config.Root()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(root, indexLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		closeErr := f.Close()
		if unlockErr != nil {
			return unlockErr
		}
		return closeErr
	}, nil
}

func replaceIndex(stage, dir string) error {
	parent := filepath.Dir(dir)
	_, statErr := os.Stat(dir)
	hasOld := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if !hasOld {
		return os.Rename(stage, dir)
	}

	backup, err := os.MkdirTemp(parent, ".stoat-index-old-*")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	if err := os.Rename(dir, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, dir); err != nil {
		if restoreErr := os.Rename(backup, dir); restoreErr != nil {
			return fmt.Errorf("replace index: %w; restore old index: %v", err, restoreErr)
		}
		return err
	}
	return os.RemoveAll(backup)
}

// LoadIndex reads the cloned index.toml under the index file lock.
func LoadIndex() (Index, error) {
	unlock, err := lockIndex()
	if err != nil {
		return Index{}, err
	}
	idx, loadErr := loadIndex(IndexDir())
	unlockErr := unlock()
	if loadErr != nil {
		return Index{}, loadErr
	}
	return idx, unlockErr
}

func loadIndex(dir string) (Index, error) {
	var idx Index
	if err := tomlx.Decode(filepath.Join(dir, "index.toml"), &idx, tomlx.Reject); err != nil {
		return Index{}, err
	}
	if idx.Schema > indexSchema {
		return Index{}, fmt.Errorf("index.toml: schema %d is newer than this stoat (%d)", idx.Schema, indexSchema)
	}
	if idx.Recipes == nil {
		idx.Recipes = map[string]IndexEntry{}
	}
	for name, entry := range idx.Recipes {
		entry.Name = name
		idx.Recipes[name] = entry
	}
	return idx, nil
}

// SearchIndex returns entries whose name or description contains term, sorted
// by name. An empty term returns the whole index.
func SearchIndex(term string) ([]IndexEntry, error) {
	unlock, err := lockIndex()
	if err != nil {
		return nil, err
	}
	if err := refreshIndexLocked(false); err != nil {
		_ = unlock()
		return nil, err
	}
	idx, err := loadIndex(IndexDir())
	unlockErr := unlock()
	if err != nil {
		return nil, err
	}
	if unlockErr != nil {
		return nil, unlockErr
	}
	q := strings.ToLower(term)
	var out []IndexEntry
	for _, entry := range idx.Recipes {
		if strings.Contains(strings.ToLower(entry.Name), q) || strings.Contains(strings.ToLower(entry.Description), q) {
			out = append(out, entry)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// IndexLookup refreshes the index and returns one entry by name.
func IndexLookup(name string) (IndexEntry, bool, error) {
	unlock, err := lockIndex()
	if err != nil {
		return IndexEntry{}, false, err
	}
	if err := refreshIndexLocked(false); err != nil {
		_ = unlock()
		return IndexEntry{}, false, err
	}
	idx, err := loadIndex(IndexDir())
	unlockErr := unlock()
	if err != nil {
		return IndexEntry{}, false, err
	}
	if unlockErr != nil {
		return IndexEntry{}, false, unlockErr
	}
	entry, ok := idx.Recipes[name]
	return entry, ok, nil
}
