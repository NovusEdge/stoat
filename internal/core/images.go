package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

// CatalogImage is the headless view of one entry in the image picker: a
// catalog entry (downloaded or not) or a local file the catalog doesn't
// claim (BYO).
//
// It exists ALONGSIDE the unexported `image` type in image.go rather than
// replacing it: `image` is what Create/resolveImage need to build a VM
// (an absolute path, an *iso.Entry, a resolved SSH user) and is built by
// resolving ONE spec string; CatalogImage is what a picker or `stoat images`
// needs to list EVERY known image with its download state, and those are
// different callers with different lifetimes, no reason for one type to
// serve both. The
// name is CatalogImage, not Image, specifically so it cannot collide with
// the existing unexported `image`.
type CatalogImage struct {
	// ID is the catalog entry's ID (see iso.Entry.ID), or "" for a local
	// file the catalog doesn't claim (BYO).
	ID string

	OS      string
	Variant string // catalog Variant; empty for BYO, which has none
	Backend string

	// File is the bare filename under isos/ once downloaded, or "" for a
	// catalog entry that has not been fetched yet. Never absolute: Images
	// only ever looks under isos/ (see LocalImages), unlike resolveImage,
	// which also accepts a browsed-to absolute path.
	File       string
	Downloaded bool

	// Bytes is the image's size, and Exact says which of two very different
	// things it is:
	//
	//   - Exact == true: Bytes is os.Stat's exact on-disk size of the
	//     downloaded file.
	//   - Exact == false: Bytes is iso.Entry.Size, a DECLARED approximation
	//     that drifts as images are rebuilt in place (see that field's doc
	//     comment); never measured, never verified.
	//
	// A caller that renders a size (or, worse, preallocates against one)
	// must not treat the two the same way; the flag exists so it doesn't
	// have to guess which it got.
	Bytes int64
	Exact bool
}

// fileSize is the exact on-disk size of a file under isos/, mirroring
// internal/tui/form.go's imageBytes. Failure is not surfaced as an error: the
// file was just listed by LocalImages and could have been removed since (or,
// for the sub-second window in Images below, is mid-download and racing with
// the reader), so a missing size just means the caller gets no exact figure
// for that entry.
func fileSize(file string) (int64, bool) {
	fi, err := os.Stat(filepath.Join(config.Root(), "isos", file))
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}

// Images lists every catalog entry (in catalog order, download state and
// size attached) followed by every local file the catalog doesn't claim, as
// BYO entries.
//
// This is the headless equivalent of internal/tui/form.go's buildImages,
// which assembles the exact same two-pass shape for the picker: reused here
// are LocalImages (image.go) for the file listing and MatchLocal (image.go)
// for entry<->file matching, so there is exactly one place (not a third
// copy) that decides which file satisfies which catalog entry. Create
// (via resolveImage) uses the same two functions, so Images can never
// disagree with what a Spec naming a catalog ID actually resolves to.
func Images() ([]CatalogImage, error) {
	files := LocalImages()
	matched := map[string]bool{}

	var out []CatalogImage
	for _, e := range iso.Catalog() {
		img := CatalogImage{ID: e.ID, OS: e.OS, Variant: e.Variant, Backend: e.Backend, Bytes: e.Size}
		if f := MatchLocal(e, files); f != "" {
			img.File = f
			img.Downloaded = true
			matched[f] = true
			// A downloaded image knows its own size exactly, so the
			// catalog's declared approximation stops being the best answer
			// available, same reasoning as buildImages.
			if n, ok := fileSize(f); ok {
				img.Bytes, img.Exact = n, true
			}
		}
		out = append(out, img)
	}

	for _, f := range files {
		if matched[f] {
			continue
		}
		backend, osName := iso.Infer(f)
		img := CatalogImage{OS: osName, Backend: backend, File: f, Downloaded: true}
		// A BYO file is local by definition, so its size is always the
		// exact one; there is no catalog entry to approximate from. Left
		// unset (Exact stays false, Bytes stays 0) only in the rare case
		// the file vanished between LocalImages' ReadDir and this Stat.
		if n, ok := fileSize(f); ok {
			img.Bytes, img.Exact = n, true
		}
		out = append(out, img)
	}
	return out, nil
}

// DownloadResult reports what actually happened to the bytes DownloadImage
// fetched, beyond the plain success/error a caller already gets from the
// returned error.
//
// Verified and ChecksumAvailable are kept as two separate facts rather than
// one bool because they answer different questions: ChecksumAvailable says
// whether a published digest existed to check against at all (some catalog
// entries, e.g. the Alpine ISOs, have none: see iso.Catalog's ChecksumURL
// comments), and Verified says whether Download actually matched the bytes
// against it. Collapsing them would make "no checksum was available" and
// "a checksum existed but wasn't confirmed" look identical, and a user
// booting a downloaded disk image deserves to know which one happened.
//
// A checksum that was available but MISMATCHED is not a third state here:
// iso.Download treats a mismatch as a hard failure, removes the partial
// file, and returns an error instead of a Release DownloadImage can report
// on, so that case surfaces through DownloadImage's error return, not
// through this struct.
type DownloadResult struct {
	// Path is the downloaded file's path relative to the data root (e.g.
	// "isos/x.iso"), exactly what iso.Download returns.
	Path string
	// Verified is true only when the downloaded bytes were checked against
	// a published digest and matched.
	Verified bool
	// ChecksumAvailable is true when a published digest existed to check
	// against, whether or not Verified ended up true. It is what lets a
	// caller tell "downloaded, nothing to verify against" apart from
	// "downloaded, verified".
	ChecksumAvailable bool
}

// downloadResult builds a DownloadResult from a Release iso.Download has
// already populated (see Release.SHA256 and Release.Verified), plus the
// path Download returned. Split out from DownloadImage so the mapping from
// iso.Release's fields to DownloadResult can be tested without a network
// call.
func downloadResult(path string, r *iso.Release) DownloadResult {
	return DownloadResult{
		Path:              path,
		Verified:          r.Verified,
		ChecksumAvailable: r.SHA256 != "",
	}
}

// DownloadImage fetches catalog entry id into isos/, reporting progress
// through progress(done, total) exactly as iso.Download does (total is 0
// when the server never sent Content-Length), and reports what verification
// actually happened (see DownloadResult).
//
// # Cancellation
//
// Cancelling ctx stops the download, it does not merely abandon it. This is
// the open item the checkpoint describes: the TUI's esc used to return control
// while leaving the goroutine reading off the socket, which is the only reason
// iso.Download needs a 60s stall timeout at all.
//
// iso.Download now takes this ctx and builds its HTTP request from it, so
// cancelling unblocks the in-flight resp.Body.Read exactly the way the stall
// timer's own cancel() already did: the plumbing was always there, it just
// was not reachable from outside. The partial file is removed on the way out
// (Download's read-error path does that), so nothing half-written survives.
//
// One honest limit: iso.Resolve, the index and checksum fetches that run
// before the body download, still uses its own client with a 30s timeout and
// takes no ctx, so a cancellation arriving during that short window is noticed when
// Resolve returns rather than interrupting it. That is a bounded 30s worst
// case on a metadata request, not an unbounded multi-minute image transfer,
// which is why it is left alone rather than widening the change.
func DownloadImage(ctx context.Context, id string, progress func(done, total int64)) (DownloadResult, error) {
	var entry iso.Entry
	found := false
	for _, e := range iso.Catalog() {
		if e.ID == id {
			entry, found = e, true
			break
		}
	}
	if !found {
		return DownloadResult{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	// Checked up front so an already-cancelled ctx costs no network at all.
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, err
	}

	release, err := iso.Resolve(entry)
	if err != nil {
		return DownloadResult{}, err
	}
	path, err := iso.Download(ctx, release, progress)
	if err != nil {
		return DownloadResult{}, err
	}
	return downloadResult(path, release), nil
}
