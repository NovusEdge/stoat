package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

// CatalogImage is the headless view of one image picker entry: a catalog entry,
// downloaded or not, or a local BYO file the catalog does not claim.
//
// It sits alongside the unexported `image` type in image.go. `image` builds one
// VM from one resolved spec; CatalogImage lists every known image with its
// download state, for a picker or `stoat images`. Named CatalogImage so it
// cannot collide with `image`.
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

	// Bytes is the image's size. Exact says which kind of number it is: true
	// means os.Stat's on-disk size of the downloaded file, false means
	// iso.Entry.Size, a declared approximation that drifts as images are rebuilt
	// in place. A caller must not treat the two the same.
	Bytes int64
	Exact bool
}

// fileSize is the on-disk size of a file under isos/, mirroring form.go's
// imageBytes. Failure is not an error: the file may have been removed since
// LocalImages listed it, or still be mid-download. A missing size means no
// exact figure for that entry.
func fileSize(file string) (int64, bool) {
	fi, err := os.Stat(filepath.Join(config.Root(), "isos", file))
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}

// Images lists every catalog entry in catalog order with download state and
// size, then every unclaimed local file as a BYO entry.
//
// It shares LocalImages and MatchLocal with form.go's buildImages and Create's
// resolveImage, so Images can never disagree with what a Spec naming a catalog
// ID resolves to.
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
			// A downloaded image's exact size replaces the catalog's
			// declared approximation, same as buildImages.
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
		// A BYO file is local, so its size is always exact; there is no
		// catalog entry to approximate from. Exact stays false and Bytes
		// stays 0 only if the file vanished between LocalImages' ReadDir
		// and this Stat.
		if n, ok := fileSize(f); ok {
			img.Bytes, img.Exact = n, true
		}
		out = append(out, img)
	}
	return out, nil
}

// DownloadResult reports what happened to the bytes DownloadImage fetched.
//
// Verified and ChecksumAvailable are separate facts. ChecksumAvailable says
// whether a published digest existed at all; some catalog entries, e.g. the
// Alpine ISOs, have none. Verified says whether Download matched the bytes
// against it. Collapsing them would hide "nothing to verify against" behind
// "not verified".
//
// A mismatched checksum is not a third state: iso.Download removes the partial
// file and returns an error, which surfaces through DownloadImage's error.
type DownloadResult struct {
	// Path is the downloaded file's path relative to the data root (e.g.
	// "isos/x.iso"), exactly what iso.Download returns.
	Path string
	// Verified is true only when the downloaded bytes were checked against
	// a published digest and matched.
	Verified bool
	// ChecksumAvailable is true when a published digest existed to check
	// against, regardless of Verified. It lets a caller tell "nothing to
	// verify against" apart from "verified".
	ChecksumAvailable bool
}

// downloadResult builds a DownloadResult from the Release iso.Download
// populated and the path it returned. Split out so the mapping tests without a
// network call.
func downloadResult(path string, r *iso.Release) DownloadResult {
	return DownloadResult{
		Path:              path,
		Verified:          r.Verified,
		ChecksumAvailable: r.SHA256 != "",
	}
}

// DownloadImage fetches catalog entry id into isos/. It reports progress
// through progress(done, total), as iso.Download does; total is 0 when the
// server sends no Content-Length. It reports what verification happened
// (see DownloadResult).
//
// # Cancellation
//
// Cancelling ctx stops the download; it does not merely abandon it. Before
// this, the TUI's esc key returned control but left the read goroutine
// running, which is why iso.Download also carries a 60s stall timeout as a
// backstop.
//
// iso.Download builds its HTTP request from this ctx, so cancelling
// unblocks the in-flight resp.Body.Read. The partial file is removed on the
// way out (Download's read-error path does that).
//
// One limit: iso.Resolve, the index and checksum fetch that runs before the
// body download, uses its own client with a fixed 30s timeout and takes no
// ctx. A cancellation arriving during that window is noticed only when
// Resolve returns. This is a bounded 30s wait on a metadata request, not
// the multi-minute image transfer, so it is left as is.
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
