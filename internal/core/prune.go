package core

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/qemu"
)

// partStaleAfter is how long a *.part file under isos/ must sit with an
// untouched mtime before Prune treats it as abandoned rather than in flight.
//
// This is not a guess dressed up as a constant — it follows from what
// internal/iso's Download already guarantees. Download resets a 60-second
// stall timer (iso.go's stallTimeout) on every read, and if that timer ever
// fires with zero progress, Download's own error path deletes the .part
// itself. So a .part still sitting on disk is in exactly one of two states:
//
//   - An active download: bytes are still landing, so the file's mtime is
//     still moving forward, always well inside 60s of "now".
//   - Abandoned: the process that owned it died before its own cleanup could
//     run — killed with SIGKILL, OOM-killed, or the machine lost power — so
//     the stall timer never got the chance to fire and delete the file for
//     us. Its mtime is frozen at whatever byte last landed.
//
// There is no third state where a live, healthy download leaves a .part
// motionless for minutes; iso.Download's own contract rules that out. The 2x
// margin here exists only to absorb scheduler jitter and filesystem mtime
// granularity, not because the 60s boundary itself is fuzzy. If
// internal/iso's stallTimeout ever changes, this should move with it — it is
// not re-derived from that unexported constant only because doing so would
// require exporting it for a single caller.
const partStaleAfter = 2 * time.Minute

// isoFieldLine and baseFieldLine best-effort extract vm.toml's `iso` and
// `base` fields out of a file that failed to parse as TOML, mirroring
// config.BrokenSSHPort's regex-over-raw-bytes approach for `sshport`. A
// broken vm.toml is usually broken by one bad line, not a wholesale
// rewrite — a hand-edit typo is the common case — so the rest of the file,
// including the field naming the image it boots, is very often still
// good text even though toml.Decode refuses the whole document.
var (
	isoFieldLine  = regexp.MustCompile(`(?m)^\s*iso\s*=\s*"([^"]*)"\s*$`)
	baseFieldLine = regexp.MustCompile(`(?m)^\s*base\s*=\s*"([^"]*)"\s*$`)
)

// PruneOpts controls what Prune is willing to remove. Every class it can act
// on defaults to off (see the Broken and Images doc comments) except the
// partial-download sweep, which needs no flag — see prunePartialDownloads.
type PruneOpts struct {
	// DryRun computes every candidate without touching disk. For an
	// operation that can delete a disk image (real bandwidth to re-fetch) or
	// a VM's disk.qcow2 (real guest state, gone for good), "show me first"
	// is the normal way to call this, not a nicety bolted on afterwards. A
	// caller (CLI, TUI) is expected to default its own flag to dry-run and
	// require an explicit confirmation to flip it — Go's zero value can't
	// encode that default for them, so it is stated here instead.
	DryRun bool

	// Broken, when true, also removes VM directories whose vm.toml exists
	// but fails to parse. Off by default: see pruneBroken.
	Broken bool

	// Images, when true, also removes files under isos/ that no VM's ISO or
	// Base field currently points at. Off by default: see pruneImages.
	Images bool
}

// Prune removes (or, with DryRun, only reports) disposable stoat state:
// broken VMs, abandoned partial downloads, and unreferenced local images —
// each gated as described on PruneOpts and on the three helper functions
// below. It returns every path acted on (or that would have been), each
// prefixed with which class it fell into, so a caller can render or log the
// decision without re-deriving it.
//
// Every helper below is independently scoped to a directory it is safe to
// touch — Root()/<broken-vm-dir>, Root()/isos/*.part, Root()/isos/* — so
// nothing here can reach id_stoat, guest_host_ed25519_key, recipes/ or
// .manifest (all top-level files/dirs under Root() outside isos/), or a VM
// directory outside Root() (config.VM.Delete's own guard, reused by
// pruneBroken, refuses that). See docs/design/core-api.md §7 and §7.1 item 8.
func Prune(opts PruneOpts) ([]string, error) {
	var removed []string

	if opts.Broken {
		r, err := pruneBroken(opts.DryRun)
		removed = append(removed, r...)
		if err != nil {
			return removed, err
		}
	}

	r, err := prunePartialDownloads(opts.DryRun)
	removed = append(removed, r...)
	if err != nil {
		return removed, err
	}

	if opts.Images {
		r, err := pruneImages(opts.DryRun)
		removed = append(removed, r...)
		if err != nil {
			return removed, err
		}
	}

	sort.Strings(removed)
	return removed, nil
}

// pruneBroken removes VM directories whose vm.toml exists but fails to
// parse. Opt-in only (PruneOpts.Broken), and this is a deliberate departure
// from what the design doc's one-line description ("remove broken VMs")
// implies by default.
//
// A parse failure is not, by itself, proof the VM is disposable. The most
// likely cause is a hand-edit typo in vm.toml — a stray quote, a duplicate
// key — and the directory next to that broken file can hold a disk.qcow2
// with hours of real guest work on it. "The config is unreadable" and "the
// VM is worthless" are different claims; only a human who looks at *why* it
// broke can tell them apart. core.Destroy already lets that human remove one
// broken VM by name once they've decided; Prune's default must not make that
// decision for them in bulk, silently, the first time they run it.
func pruneBroken(dryRun bool) ([]string, error) {
	broken, err := config.ListBroken()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, b := range broken {
		dir := filepath.Join(config.Root(), b.Name)
		bv := &config.VM{Name: b.Name, Dir: dir}

		// qemu.Running only reads v.Dir and v.PidPath() (both derivable
		// without a parsed vm.toml), so it works even though this VM's
		// config doesn't. A broken vm.toml can still belong to a VM that
		// was started before whatever edit corrupted the file — Destroy
		// already refuses to touch a running VM, and Prune, acting in bulk
		// with no one watching, must not become a quieter way around that
		// same refusal.
		if qemu.Running(bv) {
			continue
		}

		out = append(out, "broken vm: "+dir)
		if !dryRun {
			// config.VM.Delete refuses anything whose parent isn't Root(),
			// which dir always satisfies here (b.Name is a bare directory
			// entry, never containing a separator) — reused rather than
			// re-implemented so this can't drift from that guard.
			if err := bv.Delete(); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// prunePartialDownloads removes *.part files under isos/ old enough that
// they cannot be an in-flight download — see partStaleAfter for the proof.
//
// Unlike the other two classes, this one carries no opt-in flag and runs
// every time Prune is called (DryRun still gates whether it actually
// deletes). That asymmetry is deliberate, not an oversight: a stale .part
// holds no guest state and represents no deliberate choice by the user — it
// is exhaust from an interrupted fetch, and the age gate above is not a
// heuristic guess but a consequence of iso.Download's own stall-timeout
// contract. There is nothing here for a human to weigh that Broken and
// Images both require.
func prunePartialDownloads(dryRun bool) ([]string, error) {
	isosDir := filepath.Join(config.Root(), "isos")
	entries, err := os.ReadDir(isosDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".part") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			// Raced with something else removing it between ReadDir and
			// here (plausibly iso.Download's own cleanup); nothing to do.
			continue
		}
		if time.Since(fi.ModTime()) < partStaleAfter {
			// Could still be an in-flight download; see partStaleAfter for
			// why this is the one class Prune cannot mistake either way.
			continue
		}

		p := filepath.Join(isosDir, e.Name())
		out = append(out, "partial download: "+p)
		if !dryRun {
			if err := os.Remove(p); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// referencedImages returns the absolute paths of every image any VM —
// healthy or broken — currently points at, from either ISO (apkovl/ssh) or
// Base (cloudinit's overlay source).
func referencedImages() (map[string]bool, error) {
	refs := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			refs[abs] = true
		}
	}

	vms, err := config.List()
	if err != nil {
		return nil, err
	}
	for _, v := range vms {
		if v.ISO != "" {
			// ISOPath resolves a bare "isos/foo.iso" against Root() but
			// passes an absolute BYO path (outside isos/) through
			// unchanged — exactly the case that would otherwise confuse a
			// bare-filename comparison against isos/ contents.
			add(v.ISOPath())
		}
		if v.Base != "" {
			// Base is recorded absolute already (plan() sets it to
			// img.abs directly; see image.go), so no resolution needed.
			add(v.Base)
		}
	}

	// A vm.toml that fails a full TOML decode can still name a real image on
	// an otherwise-intact line, so it is best-effort regexed for `iso` and
	// `base` the same way config.BrokenSSHPort already regexes `sshport` out
	// of a broken file. A field this can't find simply does not protect an
	// image — the conservative direction, since Images only runs when a
	// caller has opted in to begin with.
	broken, err := config.ListBroken()
	if err != nil {
		return nil, err
	}
	for _, b := range broken {
		data, err := os.ReadFile(filepath.Join(config.Root(), b.Name, "vm.toml"))
		if err != nil {
			continue
		}
		if m := isoFieldLine.FindSubmatch(data); m != nil {
			iso := string(m[1])
			if filepath.IsAbs(iso) {
				add(iso)
			} else {
				add(filepath.Join(config.Root(), iso))
			}
		}
		if m := baseFieldLine.FindSubmatch(data); m != nil {
			add(string(m[1]))
		}
	}
	return refs, nil
}

// pruneImages removes files under isos/ that no VM — healthy or broken —
// currently references. Opt-in only (PruneOpts.Images).
//
// An unreferenced image is not evidence of a mistake. It is, overwhelmingly,
// evidence of the opposite: someone deliberately spent real bandwidth (these
// are hundreds of MB) downloading it, has not yet built a VM from it, and
// may do exactly that tomorrow. "No VM points at this today" and "this is
// garbage" are not the same claim, and only the caller — not this package —
// knows which one is true for a given file. Defaulting this on would
// optimise for reclaiming disk space at the cost of occasionally forcing a
// multi-minute re-download the user did nothing to deserve; defaulting it
// off is what keeps that trade a choice instead of a surprise.
func pruneImages(dryRun bool) ([]string, error) {
	refs, err := referencedImages()
	if err != nil {
		return nil, err
	}

	var out []string
	for _, f := range LocalImages() {
		abs, err := filepath.Abs(filepath.Join(config.Root(), "isos", f))
		if err != nil {
			continue
		}
		if refs[abs] {
			continue
		}

		out = append(out, "orphaned image: "+abs)
		if !dryRun {
			if err := os.Remove(abs); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
