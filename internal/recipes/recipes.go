// Package recipes manages the shell/cloud-config scripts stoat runs inside
// guests.
package recipes

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

//go:embed *.sh *.yaml
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
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		return err
	}
	items, err := bundled.ReadDir(".")
	if err != nil {
		return err
	}
	man := readManifest()
	next := map[string]string{}
	for _, it := range items {
		name := it.Name()
		want, err := bundled.ReadFile(name)
		if err != nil {
			return err
		}
		wantSum := sum(want)
		dst := filepath.Join(dir(), name)
		have, err := os.ReadFile(dst)
		switch {
		case os.IsNotExist(err): // new recipe, or a fresh install
		case err != nil:
			return err
		case sum(have) == wantSum: // already current
			next[name] = wantSum
			continue
		case man == nil:
			// Pre-manifest: unknowable whether this was edited, so keep a
			// copy. ".bak" is deliberately not a suffix List matches.
			if err := os.WriteFile(dst+".bak", have, 0o644); err != nil {
				return err
			}
		case man[name] != sum(have):
			// Edited by hand (or hand-created before stoat bundled a recipe
			// of this name, which is the same thing as far as this goes).
			// Leave it, and record nothing: writing the bundled sum here
			// would make the next Install read the edit as stoat's own copy
			// and overwrite it.
			continue
		}
		if err := os.WriteFile(dst, want, 0o755); err != nil {
			return err
		}
		next[name] = wantSum
	}
	return writeManifest(next)
}

// List returns installed recipe names offered for osName on backend.
//
// Shell recipes are named "xfce.<os>.sh" and only make sense pushed
// interactively over a live shell, so they're offered on the apkovl/ssh
// backends, filtered to the exact matching OS. The cloud-config fragment
// ("xfce.cloud.yaml") only makes sense merged into a cloud-init seed, so
// it's offered only on the cloudinit backend, filtered to
// guest.Lookup(osName).CloudRecipes, unless a per-OS fragment
// ("xfce.fedora.cloud.yaml") exists for that OS. A recipe with no file for
// the (osName, backend) combination is not offered, e.g.
// List("ubuntu", "cloudinit") never returns xfce.ubuntu.sh, and
// List("alpine", "apkovl") never returns the cloud fragment.
//
// CloudRecipes is a single bool per OS, but "the shared fragment set" is
// really cloud-init's packages: list having no per-distro syntax: one
// fragment only works across OSes whose package names happen to match, so an
// OS can be safe for one shared fragment and not another (see
// guest.OS.CloudRecipes and devtools.cloud.yaml vs xfce.cloud.yaml). Alpine
// is CloudRecipes-eligible (devtools.cloud.yaml installs fine on apk) but
// still needs its own xfce.alpine.cloud.yaml, because xfce.cloud.yaml's
// systemctl runcmd does not run under Alpine's OpenRC. So, unlike Fedora
// (kept out of the shared set entirely), a per-OS override and CloudRecipes
// being true for the same OS both happen at once here. The first loop below
// records which base names have a per-OS override for osName so the second
// loop can suppress the shared fragment for exactly those names: without
// it, an OS with both would be offered two entries for the same recipe,
// the shared one (wrongly in scope) and the per-OS one.
//
// Fedora is kept out of the shared set entirely for a second, independent
// reason that still matters even with per-OS suppression: it would silently
// break any shared-only fragment (no per-OS override) whose package names
// happen not to hold on dnf. devtools.cloud.yaml is one example:
// git/curl/ca-certificates/tmux/less match, but Fedora's vim package is
// "vim-enhanced", not "vim".
func List(osName, backend string) ([]string, error) {
	entries, err := os.ReadDir(dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// overridden collects the base name of every per-OS fragment that
	// matches osName ("xfce.alpine.cloud.yaml" -> "xfce"), so the shared
	// fragment of the same name is suppressed below rather than offered
	// alongside it.
	overridden := map[string]bool{}
	if backend == "cloudinit" {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".cloud.yaml") {
				continue
			}
			base := strings.TrimSuffix(name, ".cloud.yaml")
			i := strings.LastIndex(base, ".")
			if i >= 0 && base[i+1:] == osName {
				overridden[base[:i]] = true
			}
		}
	}

	var out []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case backend == "cloudinit" && strings.HasSuffix(name, ".cloud.yaml"):
			// "xfce.fedora.cloud.yaml" -> per-OS, offered only to fedora.
			// "xfce.cloud.yaml"        -> shared, offered to CloudRecipes
			// OSes that don't have their own override for "xfce".
			base := strings.TrimSuffix(name, ".cloud.yaml")
			if i := strings.LastIndex(base, "."); i >= 0 {
				if base[i+1:] == osName {
					out = append(out, name)
				}
			} else if g, ok := guest.Lookup(osName); ok && g.CloudRecipes && !overridden[base] {
				out = append(out, name)
			}
		case backend != "cloudinit" && strings.HasSuffix(name, ".sh"):
			// "xfce.alpine.sh" -> "alpine"
			fields := strings.Split(strings.TrimSuffix(name, ".sh"), ".")
			fileOS := fields[len(fields)-1]
			if fileOS == osName {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Read returns a recipe's body. name is the full filename as returned by
// List.
func Read(name string) (string, error) {
	b, err := os.ReadFile(Path(name))
	return string(b), err
}
