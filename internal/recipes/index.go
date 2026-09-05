package recipes

import (
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
)

const indexStampName = ".fetched"

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

func IndexURL() string { return os.Getenv("STOAT_INDEX") }

func IndexDir() string { return filepath.Join(config.Root(), "index") }

func RefreshIndex(bool) error { return nil }

func LoadIndex() (Index, error) { return Index{}, nil }

func SearchIndex(string) ([]IndexEntry, error) { return nil, nil }

func IndexLookup(string) (IndexEntry, bool, error) { return IndexEntry{}, false, nil }

func writeLockStub(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}
