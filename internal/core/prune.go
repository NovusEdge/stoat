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

// partStaleAfter is how long a *.part file under isos/ sits with no mtime
// change before Prune treats it as abandoned.
//
// iso.Download resets a 60-second stall timer on every read. If the timer
// fires with zero progress, Download deletes the .part itself. So a .part
// still on disk is in exactly one of two states: an active download, mtime
// moving and always under 60s old, or an abandoned one, mtime frozen because
// the owning process died before its own cleanup ran. No third state exists.
//
// The 2x margin absorbs scheduler jitter and mtime granularity. It is not a
// guess at the 60s boundary itself. If internal/iso's stallTimeout changes,
// update this too; it is not derived from that unexported constant to avoid
// exporting it for one caller.
const partStaleAfter = 2 * time.Minute

// isoFieldLine and baseFieldLine extract vm.toml's `iso` and `base` fields
// by regex when the file fails to parse as TOML. config.BrokenSSHPort uses
// the same approach for `sshport`.
//
// A broken vm.toml is usually broken by one bad line, from a hand-edit
// typo. The rest of the file, including the image field, is often still
// valid text even though toml.Decode refuses the whole document.
var (
	isoFieldLine  = regexp.MustCompile(`(?m)^\s*iso\s*=\s*"([^"]*)"\s*$`)
	baseFieldLine = regexp.MustCompile(`(?m)^\s*base\s*=\s*"([^"]*)"\s*$`)
)

// PruneItem is one thing Prune removed, or would remove under DryRun.
//
// Class is snake_case: it is also the wire value for the JSON contract's
// "prune" command (docs/design/json-contract-draft.md §3.3). A caller that
// wants prose renders Class itself, instead of parsing a formatted string.
type PruneItem struct {
	// Class is one of "broken_vm", "partial_download" or "orphaned_image",
	// matching pruneBroken, prunePartialDownloads and pruneImages respectively.
	Class string
	// Path is the absolute path acted on (or that would have been, under
	// DryRun).
	Path string
}

const (
	classBrokenVM        = "broken_vm"
	classPartialDownload = "partial_download"
	classOrphanedImage   = "orphaned_image"
)

// PruneOpts controls what Prune is willing to remove. Every class it can act
// on defaults to off (see the Broken and Images doc comments) except the
// partial-download sweep, which needs no flag; see prunePartialDownloads.
type PruneOpts struct {
	// DryRun computes every candidate without touching disk.
	//
	// Prune can delete a disk image (real bandwidth to re-fetch) or a VM's
	// disk.qcow2 (guest state, gone for good). A caller (CLI, TUI) should
	// default its own flag to dry-run and require explicit confirmation to
	// disable it. Go's zero value cannot encode that default, so it is
	// stated here instead.
	DryRun bool

	// Broken, when true, also removes VM directories whose vm.toml exists
	// but fails to parse. Off by default: see pruneBroken.
	Broken bool

	// Images, when true, also removes files under isos/ that no VM's ISO or
	// Base field currently points at. Off by default: see pruneImages.
	Images bool
}

// Prune removes, or with DryRun only reports, disposable stoat state:
// broken VMs, abandoned partial downloads, and unreferenced local images.
// PruneOpts and the three helpers below gate each class. Prune returns
// every item acted on, each tagged with its class, so a caller can render
// or log the decision without parsing a formatted string.
//
// Each helper only touches one scoped directory: Root()/<broken-vm-dir>,
// Root()/isos/*.part, or Root()/isos/*. Prune never reaches id_stoat,
// guest_host_ed25519_key, recipes/, .manifest, or a VM directory outside
// Root(); config.VM.Delete's own guard enforces the last one. See
// docs/design/core-api.md §7 and §7.1 item 8.
func Prune(opts PruneOpts) ([]PruneItem, error) {
	// The same data-root lock Create, Clone and Destroy take. Prune decides
	// what is unreferenced by reading every vm.toml. Without the lock, an
	// image an in-flight Create just picked could be judged orphaned and
	// deleted before that Create writes the vm.toml protecting it.
	// Held for DryRun too, so the report matches what a real run would remove.
	unlock, err := config.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	var removed []PruneItem

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

	// Sorts by the formatted "<prefix>: <path>" line, the key the old
	// []string return used, not by class then path. This keeps the CLI's
	// rendered output byte-identical to before PruneItem existed.
	prefix := map[string]string{
		classBrokenVM:        "broken vm: ",
		classPartialDownload: "partial download: ",
		classOrphanedImage:   "orphaned image: ",
	}
	sort.Slice(removed, func(i, j int) bool {
		return prefix[removed[i].Class]+removed[i].Path < prefix[removed[j].Class]+removed[j].Path
	})
	return removed, nil
}

// pruneBroken removes VM directories whose vm.toml exists but fails to
// parse. Opt-in only (PruneOpts.Broken).
//
// A parse failure does not prove the VM is disposable. The likely cause is
// a hand-edit typo, and the directory can still hold a disk.qcow2 with real
// guest work on it. Destroy already lets a human remove one broken VM by
// name after they decide. Prune's default must not make that call in bulk.
func pruneBroken(dryRun bool) ([]PruneItem, error) {
	broken, err := config.ListBroken()
	if err != nil {
		return nil, err
	}
	var out []PruneItem
	for _, b := range broken {
		dir := filepath.Join(config.Root(), b.Name)
		bv := &config.VM{Name: b.Name, Dir: dir}

		// qemu.Running only needs v.Dir and v.PidPath(), both derivable
		// without a parsed vm.toml, so it works on a broken VM too. The VM
		// may have started before the edit that broke vm.toml. Destroy
		// refuses to touch a running VM; Prune must refuse the same way,
		// even acting in bulk.
		if qemu.Running(bv) {
			continue
		}

		out = append(out, PruneItem{Class: classBrokenVM, Path: dir})
		if !dryRun {
			// config.VM.Delete refuses any path whose parent isn't Root().
			// dir always satisfies that: b.Name is a bare directory entry,
			// never a path with a separator.
			if err := bv.Delete(); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// prunePartialDownloads removes *.part files under isos/ old enough that
// they cannot be an in-flight download; see partStaleAfter.
//
// Unlike Broken and Images, this class has no opt-in flag; it runs every
// time Prune is called, and DryRun only gates the actual delete. A stale
// .part holds no guest state and represents no user choice, it is exhaust
// from an interrupted fetch. There is nothing here for a human to weigh.
func prunePartialDownloads(dryRun bool) ([]PruneItem, error) {
	isosDir := filepath.Join(config.Root(), "isos")
	entries, err := os.ReadDir(isosDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []PruneItem
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
		out = append(out, PruneItem{Class: classPartialDownload, Path: p})
		if !dryRun {
			if err := os.Remove(p); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// referencedImages returns the absolute paths of every image any VM
// (healthy or broken) currently points at, from either ISO (apkovl/ssh) or
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
			// ISOPath resolves a bare "isos/foo.iso" against Root(), but
			// passes an absolute BYO path through unchanged. A
			// bare-filename comparison against isos/ contents would
			// otherwise confuse the two.
			add(v.ISOPath())
		}
		if v.Base != "" {
			// Base is recorded absolute already (plan() sets it to
			// img.abs directly; see image.go), so no resolution needed.
			add(v.Base)
		}
	}

	// A vm.toml that fails a full TOML decode can still name a real image on
	// an otherwise-intact line, regexed here for `iso` and `base` the same
	// way config.BrokenSSHPort regexes `sshport`. A field this can't find
	// simply does not protect an image, the safe direction since Images
	// requires opt-in.
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

// pruneImages removes files under isos/ that no VM, healthy or broken,
// currently references. Opt-in only (PruneOpts.Images).
//
// An unreferenced image is usually not a mistake. Someone spent real
// bandwidth, hundreds of MB, downloading it and may build a VM from it
// later. Only the caller knows whether a given file is garbage or a
// deliberate download waiting to be used. Defaulting this off keeps that
// call a choice, not a surprise re-download.
func pruneImages(dryRun bool) ([]PruneItem, error) {
	refs, err := referencedImages()
	if err != nil {
		return nil, err
	}

	var out []PruneItem
	for _, f := range LocalImages() {
		abs, err := filepath.Abs(filepath.Join(config.Root(), "isos", f))
		if err != nil {
			continue
		}
		if refs[abs] {
			continue
		}

		out = append(out, PruneItem{Class: classOrphanedImage, Path: abs})
		if !dryRun {
			if err := os.Remove(abs); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
