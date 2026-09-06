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

// ManifestName is the file in the recipes directory recording the checksum
// of every recipe stoat itself wrote there. It lets Install tell "this is
// stoat's copy, from an older release" from "the user edited this", which a
// bare existence check cannot.
//
// Named with a leading dot, so List's extension matching and the editor
// escape hatch both ignore it without needing to know it exists.
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
// an earlier stoat stayed on disk forever, so a fixed recipe never reached
// anyone who already had the buggy one. One shipped bug: xfce.cloud.yaml
// installed a desktop with no X server, autologged root into a failing
// startx, and left a black screen. It kept shipping to existing users long
// after the bundled copy was fixed.
//
// Install compares three things: what is bundled now, what is on disk, and
// what stoat recorded writing (ManifestName). A file whose contents still
// match the manifest is stoat's own, untouched, and gets refreshed. A file
// that differs from the manifest was edited by hand and is left exactly as
// it is. Editing a recipe in place is a supported workflow (see
// tui/recipesdir.go); silently reverting someone's edit is worse than
// leaving them a stale recipe.
//
// The upgrade case has no manifest to consult: every recipe on disk predates
// it, so an edited one cannot be told from an untouched one. Those get
// refreshed too, but the old contents are kept alongside as "<name>.bak"
// first, so the one-time adoption destroys nothing. It happens once; after
// that the manifest is authoritative and edits are recognised.
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
	if err := sweepV1(); err != nil {
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

// v1AtticName holds the flat recipe files v1 left in the recipes directory.
// A dotfile so nothing lists it, and so a second sweep skips it like any
// other dotfile.
const v1AtticName = ".v1-removed"

// sweepV1 moves v1's flat recipe files out of the recipes directory, once.
//
// v1 kept a recipe in a single file whose target OS was encoded in its name
// ("xfce.alpine.sh", "xfce.cloud.yaml"). v2 keeps each recipe in a directory
// with a recipe.toml. Nothing reads the old format, so an upgrade otherwise
// leaves a directory holding both formats. Fifteen dead files would sit
// next to four live directories, all looking equally authoritative to
// someone opening the folder to write a recipe.
//
// The rule is the v2 invariant, not a list of v1 names: a recipe is a
// directory, so any regular file here is not a recipe. This carries no
// knowledge of the old format and keeps working for whatever v1 shipped
// across its life, including the .bak files Install used to leave behind.
//
// Files are moved, not deleted. They are cheap to keep, and one might be
// something the user wrote by hand, which no rule here can tell from
// something stoat shipped. The attic is a dotfile, so the recipes directory
// reads as pure v2 either way. `rm -rf ~/.stoat/recipes/.v1-removed`
// finishes the job for anyone who wants the disk back.
func sweepV1() error {
	entries, err := os.ReadDir(dir())
	if err != nil {
		return err
	}
	var stale []string
	for _, e := range entries {
		// Dotfiles are stoat's own bookkeeping (ManifestName, and this
		// attic); directories are v2 recipes.
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		stale = append(stale, e.Name())
	}
	if len(stale) == 0 {
		return nil
	}

	man := readManifest()
	attic := filepath.Join(dir(), v1AtticName)
	if err := os.MkdirAll(attic, 0o755); err != nil {
		return err
	}
	for _, name := range stale {
		// A flat file whose contents still match what stoat recorded writing is
		// stoat's own v1 copy, swept quietly. Anything else is a recipe the
		// user wrote or edited; warn, so a swept recipe does not just vanish
		// from the picker with no explanation.
		if !sweptIsStoats(name, man) {
			logx.L().Warn("moved a legacy recipe out of the recipes directory; convert it to the v2 format to keep using it",
				"recipe", name, "moved_to", v1AtticName, "docs", "docs/writing-recipes.md")
		}
		// An existing attic entry from an earlier sweep wins: it is the older
		// copy, and overwriting it with a file the user has since re-created
		// would lose the thing worth keeping.
		dst := filepath.Join(attic, name)
		if _, err := os.Stat(dst); err == nil {
			if err := os.Remove(filepath.Join(dir(), name)); err != nil {
				return err
			}
			continue
		}
		if err := os.Rename(filepath.Join(dir(), name), dst); err != nil {
			return err
		}
	}
	return nil
}

// sweptIsStoats reports whether the flat file name is stoat's own v1 copy: its
// contents still match the checksum man recorded. A file man does not list, or
// one whose contents changed, is treated as the user's.
func sweptIsStoats(name string, man map[string]string) bool {
	want, ok := man[name]
	if !ok {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir(), name))
	if err != nil {
		return false
	}
	return sum(b) == want
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

// installFile installs a single bundled file, keyed by key: its path
// relative to the recipes root, a bare filename for the old flat format or
// "<recipe>/<file>" for a v2 recipe directory. It is the shared body of the
// refresh-vs-preserve-edits rule from Install's doc comment: refresh a copy
// that still matches what stoat last wrote, or predates the manifest and
// gets backed up first. Leave anything else alone.
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
		// Edited by hand, or hand-created before stoat bundled a recipe
		// of this name: the same case either way. Leave it, and record
		// nothing. Writing the bundled sum here would make the next
		// Install read the edit as stoat's own copy and overwrite it.
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

// ListManifests scans every root for v2 recipes: subdirectories holding a
// recipe.toml. Unlike List, it does not filter by OS or backend. A caller that needs that filters against the parsed Manifest's
// OS/Requires fields (see MatchesVM).
//
// A subdirectory with no recipe.toml, a stray directory or leftover .bak
// territory, is silently skipped. A directory that is a recipe but fails to
// parse is claimed by its root and logged, so a lower-priority recipe cannot
// replace it.
func ListManifests() ([]Manifest, error) {
	locks, err := lockRecipeScopes(false)
	if err != nil {
		return nil, err
	}
	manifests, readErr := listManifestsLocked()
	unlockErr := unlockRecipeScopes(locks)
	if readErr != nil {
		return nil, readErr
	}
	return manifests, unlockErr
}

func listManifestsLocked() ([]Manifest, error) {
	seen := map[string]bool{}
	var out []Manifest
	roots, err := Roots()
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			owned, err := owns(root, e.Name())
			if err != nil {
				return nil, err
			}
			if !owned {
				continue
			}
			path := filepath.Join(root.Path, e.Name(), "recipe.toml")
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			seen[e.Name()] = true
			m, err := ParseManifest(path)
			if err != nil {
				logx.L().Warn("skipping recipe with an invalid manifest", "dir", e.Name(), "err", err)
				continue
			}
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ScriptBody returns the script a recipe runs on osName. The recipe resolves
// through its manifest to install.sh, or the per-OS override the manifest
// declares. A name with no recipe.toml is not a recipe and returns an error.
func ScriptBody(name, osName string) (string, error) {
	m, ok, err := ManifestFor(name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no such recipe %q", name)
	}
	return m.ScriptContent(osName)
}

// RuntimeFor returns the interpreter that runs name's script: the manifest's
// Runtime field. A name with no recipe.toml is not a recipe and returns an
// error.
func RuntimeFor(name, osName string) (string, error) {
	m, ok, err := ManifestFor(name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no such recipe %q", name)
	}
	return m.Runtime, nil
}

// ScriptHash returns the hex sha256 of ScriptBody(name, osName). A caller
// compares it against a stored AppliedRecipe.Hash to tell whether a "once"
// recipe's script changed since it last ran, even at the same manifest
// version.
func ScriptHash(name, osName string) (string, error) {
	body, err := ScriptBody(name, osName)
	if err != nil {
		return "", err
	}
	return sum([]byte(body)), nil
}
