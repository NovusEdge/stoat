// Package recipes manages the shell/cloud-config scripts stoat runs inside
// guests.
package recipes

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/logx"
)

//go:embed bundled
var bundled embed.FS

func dir() string { return filepath.Join(config.Root(), "recipes") }

// Path is the on-disk location of a recipe. name is the full filename
// (including its .sh or .yaml extension) as returned by List.
func Path(name string) string { return filepath.Join(dir(), name) }

// ManifestName is the file in the recipes directory recording the checksum of
// every recipe stoat itself wrote there. It is what lets Install tell "this is
// stoat's copy, from an older release" from "the user edited this", which a
// bare existence check cannot.
//
// Named with a leading dot so List's extension matching and the editor escape
// hatch both ignore it without needing to know it exists.
const ManifestName = ".manifest"

// readManifest returns name -> checksum for the recipes stoat last wrote. A
// missing or unreadable manifest is not an error: it means "written by a stoat
// from before the manifest existed", which Install handles as its own case.
func readManifest() map[string]string {
	b, err := os.ReadFile(filepath.Join(dir(), ManifestName))
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			m[f[1]] = f[0]
		}
	}
	return m
}

func writeManifest(m map[string]string) error {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", m[n], n)
	}
	return os.WriteFile(filepath.Join(dir(), ManifestName), []byte(b.String()), 0o644)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Install copies bundled recipes into the data root, refreshing stale copies
// while leaving local edits alone.
//
// Never overwriting was the old rule, and it aged badly: a recipe shipped by
// an earlier stoat stays on disk forever, so a fixed recipe never reaches
// anyone who already ran the buggy one. That is not hypothetical: the
// xfce.cloud.yaml that installed a desktop with no X server, autologged root
// into a failing startx and left a black screen went on being served to
// everyone who had it, long after the bundled copy was fixed.
//
// So Install compares three things: what is bundled now, what is on disk, and
// what stoat recorded writing (ManifestName). A file whose contents still
// match the manifest is stoat's own, untouched, and gets refreshed. A file
// that differs from the manifest was edited by hand and is left exactly as it
// is; authoring a recipe by editing one in place is a supported workflow (see
// tui/recipesdir.go), and silently reverting someone's work is worse than
// shipping them a stale recipe.
//
// The upgrade case has no manifest to consult: every recipe on disk predates
// it, and there is no way to tell an edited one from an untouched one. Those
// get refreshed too, but the old contents are kept alongside as "<name>.bak"
// first, so the one-time adoption cannot destroy anything. It happens once,
// and afterwards the manifest is authoritative and edits are recognised.
func Install() error {
	sub, err := fs.Sub(bundled, "bundled")
	if err != nil {
		return err
	}
	return install(sub)
}

// install does the work behind Install, against src rather than the
// package-level embed.FS, so the copy-and-refresh logic can be exercised
// against a fake tree (fstest.MapFS) in tests without needing real bundled
// recipes on disk.
//
// src's top level holds v2 recipe directories ("xfce/recipe.toml",
// "xfce/install.sh"). Each is installed with the refresh-vs-preserve-edits
// rule: a file whose checksum matches the manifest is stoat's own and gets
// refreshed, while an edited file is left alone.
func install(src fs.FS) error {
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	items, err := fs.ReadDir(src, ".")
	if err != nil {
		return err
	}
	man := readManifest()
	next := map[string]string{}
	for _, it := range items {
		if !it.IsDir() {
			continue // v2 only has directories
		}
		if err := installDir(src, it.Name(), man, next); err != nil {
			return err
		}
	}
	return writeManifest(next)
}

// installDir installs every file under a v2 recipe directory (name), keyed
// by its path relative to the recipes root ("xfce/recipe.toml"), mirroring
// that directory structure into dir().
func installDir(src fs.FS, name string, man, next map[string]string) error {
	return fs.WalkDir(src, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir(), filepath.FromSlash(p)), 0o755)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		return installFile(src, p, mode, man, next)
	})
}

// installFile installs a single bundled file, keyed by key (its path
// relative to the recipes root: a bare filename for the old flat format, or
// "<recipe>/<file>" for a v2 recipe directory). It is the shared body of
// the refresh-vs-preserve-edits rule Install's doc comment describes:
// refresh a copy that still matches what stoat last wrote (or predates the
// manifest, backed up first), leave anything else alone.
func installFile(src fs.FS, key string, mode os.FileMode, man, next map[string]string) error {
	want, err := fs.ReadFile(src, key)
	if err != nil {
		return err
	}
	wantSum := sum(want)
	dst := filepath.Join(dir(), filepath.FromSlash(key))
	have, err := os.ReadFile(dst)
	switch {
	case os.IsNotExist(err): // new recipe, or a fresh install
	case err != nil:
		return err
	case sum(have) == wantSum: // already current
		next[key] = wantSum
		return nil
	case man == nil:
		// Pre-manifest: unknowable whether this was edited, so keep a
		// copy. ".bak" is deliberately not a suffix List matches.
		if err := os.WriteFile(dst+".bak", have, 0o644); err != nil {
			return err
		}
	case man[key] != sum(have):
		// Edited by hand (or hand-created before stoat bundled a recipe
		// of this name, which is the same thing as far as this goes).
		// Leave it, and record nothing: writing the bundled sum here
		// would make the next Install read the edit as stoat's own copy
		// and overwrite it.
		return nil
	}
	if err := os.WriteFile(dst, want, mode); err != nil {
		return err
	}
	next[key] = wantSum
	return nil
}

// List returns installed recipe names offered for osName. Backend is ignored
// in v2: every recipe is a shell script, and the backend (apkovl, ssh,
// cloudinit) determines HOW it runs, not WHETHER it applies.
//
// Filtering is by the manifest's OS and Requires fields (see MatchesVM).
func List(osName, _ string) ([]string, error) {
	manifests, err := ListManifests()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range manifests {
		if MatchesVM(&m, osName) {
			out = append(out, m.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ListManifests scans dir() for v2 recipes: subdirectories holding a
// recipe.toml (docs/recipe-spec-v2.md). Unlike List, it does not filter by
// OS or backend; a caller that needs that does it against the parsed
// Manifest's OS/Requires fields, the way UnsupportedReason already does for
// v1's front-matter Metadata.
//
// A subdirectory that isn't a v2 recipe (no recipe.toml: stray directory,
// or leftover .bak territory) is silently skipped. One that IS a recipe
// directory but fails to parse is also skipped, but logged: a single typo'd
// manifest should not take every other recipe down with it.
func ListManifests() ([]Manifest, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir(), e.Name(), "recipe.toml")
		if _, err := os.Stat(path); err != nil {
			continue // not a v2 recipe directory
		}
		m, err := ParseManifest(path)
		if err != nil {
			logx.L().Warn("skipping recipe with an invalid manifest", "dir", e.Name(), "err", err)
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read returns a recipe's body. name is the full filename as returned by
// List.
func Read(name string) (string, error) {
	b, err := os.ReadFile(Path(name))
	return string(b), err
}
