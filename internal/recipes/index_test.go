package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/testutil"
)

const sampleIndex = `schema = 1

[recipes.tailscale]
source      = "https://example.invalid/x/stoat-tailscale"
description = "join a tailnet on boot"
os          = ["alpine", "debian"]

[recipes.xfce]
source      = "https://example.invalid/x/stoat-xfce"
description = "a desktop over vnc"
os          = ["debian"]
`

func indexRoot(t *testing.T) {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("STOAT_INDEX", testutil.GitRepo(t, map[string]string{"index.toml": sampleIndex}))
}

func TestRefreshIndexClonesAndLoads(t *testing.T) {
	indexRoot(t)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if idx.Recipes["tailscale"].Source == "" {
		t.Fatalf("index = %+v", idx)
	}
}

func TestRefreshIndexIsSkippedWhenFresh(t *testing.T) {
	indexRoot(t)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	stamp := filepath.Join(IndexDir(), indexStampName)
	before, err := os.Stat(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := RefreshIndex(false); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a fresh index was fetched again")
	}
}

func TestRefreshIndexUsesChangedSourceWhenCacheIsFresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	t.Chdir(t.TempDir())
	sourceA := testutil.GitRepo(t, map[string]string{"index.toml": sampleIndex})
	sourceB := testutil.GitRepo(t, map[string]string{"index.toml": `schema = 1

[recipes.other]
source = "https://example.invalid/x/stoat-other"
description = "only from source B"
os = ["alpine"]
`})
	t.Setenv("STOAT_INDEX", sourceA)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STOAT_INDEX", sourceB)
	got, err := SearchIndex("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "other" {
		t.Fatalf("fresh cache after source change = %+v, want source B", got)
	}
}

func TestRefreshIndexFetchesAStaleIndex(t *testing.T) {
	indexRoot(t)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	stamp := filepath.Join(IndexDir(), indexStampName)
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(stamp, old, old); err != nil {
		t.Fatal(err)
	}
	indexSource := os.Getenv("STOAT_INDEX")
	testutil.GitCommit(t, indexSource, map[string]string{
		"index.toml": strings.Replace(sampleIndex, "join a tailnet on boot", "updated description", 1),
	}, "")
	if err := RefreshIndex(false); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.Recipes["tailscale"].Description; got != "updated description" {
		t.Fatalf("stale index description = %q, want updated description", got)
	}
}

func TestRefreshIndexFailureKeepsUsableCache(t *testing.T) {
	indexRoot(t)
	if err := RefreshIndex(true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STOAT_INDEX", filepath.Join(t.TempDir(), "missing-index.git"))
	if err := RefreshIndex(true); err == nil {
		t.Fatal("RefreshIndex unexpectedly succeeded for a missing local source")
	}
	idx, err := LoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if idx.Recipes["tailscale"].Source == "" {
		t.Fatalf("cached index was lost after refresh failure: %+v", idx)
	}
}

func TestSearchIndexMatchesNameAndDescription(t *testing.T) {
	indexRoot(t)
	got, err := SearchIndex("tailnet")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "tailscale" {
		t.Fatalf("results = %+v", got)
	}
}

func TestIndexLookupUnknownName(t *testing.T) {
	indexRoot(t)
	_, ok, err := IndexLookup("tailscal")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("tailscal resolved")
	}
}
