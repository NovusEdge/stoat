package core

import (
	"context"
	"errors"
	"testing"

	"github.com/novusedge/stoat/internal/iso"
)

// TestImagesListsCatalogAndBYO pins the two-pass shape Images promises:
// every catalog entry first (in catalog order), then local files the
// catalog doesn't claim, as BYO entries.
func TestImagesListsCatalogAndBYO(t *testing.T) {
	dir := root(t)
	// alpine-virt is matched via iso.Infer (Flavor set, no direct URL), same
	// path MatchLocal exercises in image_test.go-adjacent tests elsewhere.
	haveImage(t, dir, "alpine-virt-3.24.1-x86_64.iso")
	haveImage(t, dir, "custom.qcow2")

	got, err := Images()
	if err != nil {
		t.Fatal(err)
	}

	catalogLen := len(iso.Catalog())
	if len(got) != catalogLen+1 {
		t.Fatalf("len(Images()) = %d, want %d catalog entries + 1 BYO", len(got), catalogLen+1)
	}

	var virt, byo *CatalogImage
	for i := range got {
		switch {
		case got[i].ID == "alpine-virt":
			virt = &got[i]
		case got[i].ID == "" && got[i].File == "custom.qcow2":
			byo = &got[i]
		}
	}
	if virt == nil {
		t.Fatal("alpine-virt not found in Images()")
	}
	if !virt.Downloaded || virt.File != "alpine-virt-3.24.1-x86_64.iso" {
		t.Errorf("alpine-virt = %+v, want Downloaded with the matched file", virt)
	}
	if !virt.Exact || virt.Bytes != 1 { // haveImage writes a 1-byte file
		t.Errorf("alpine-virt size = %d/%v, want exact 1-byte on-disk size", virt.Bytes, virt.Exact)
	}

	if byo == nil {
		t.Fatal("custom.qcow2 not found in Images() as a BYO entry")
	}
	if !byo.Downloaded || !byo.Exact || byo.Bytes != 1 {
		t.Errorf("custom.qcow2 = %+v, want a downloaded BYO entry with exact size 1", byo)
	}
}

// TestImagesUndownloadedEntryReportsDeclaredSize covers a catalog entry with
// no matching local file: File must stay empty and the size must be the
// catalog's declared approximation (Exact == false), never treated as if it
// were measured.
func TestImagesUndownloadedEntryReportsDeclaredSize(t *testing.T) {
	root(t)

	got, err := Images()
	if err != nil {
		t.Fatal(err)
	}
	var entry *CatalogImage
	for i := range got {
		if got[i].ID == "alpine-cloud" {
			entry = &got[i]
		}
	}
	if entry == nil {
		t.Fatal("alpine-cloud not found in Images()")
	}
	if entry.Downloaded || entry.File != "" {
		t.Errorf("alpine-cloud = %+v, want not downloaded and no file", entry)
	}
	if entry.Exact {
		t.Error("alpine-cloud.Exact = true, want false: nothing on disk to measure")
	}
	var want int64
	for _, e := range iso.Catalog() {
		if e.ID == "alpine-cloud" {
			want = e.Size
		}
	}
	if entry.Bytes != want {
		t.Errorf("alpine-cloud.Bytes = %d, want the catalog's declared %d", entry.Bytes, want)
	}
}

// TestDownloadImageUnknownID covers the lookup path: an id with no matching
// catalog entry must fail as ErrNotFound without ever touching the network.
func TestDownloadImageUnknownID(t *testing.T) {
	root(t)
	err := DownloadImage(context.Background(), "no-such-catalog-entry", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestDownloadImageHonoursAlreadyCancelledContext covers the one form of
// cancellation DownloadImage can test without a network: ctx checked and
// honoured BEFORE iso.Resolve/iso.Download are ever called. A context that
// is already cancelled when DownloadImage is entered must return that error
// immediately rather than dialing out; this is the only guarantee this
// function's doc comment makes about cancellation being complete; anything
// mid-flight is honestly documented there as returning control, not
// stopping the work, and is not testable here for exactly that reason (it
// would require a live download to interrupt).
func TestDownloadImageHonoursAlreadyCancelledContext(t *testing.T) {
	root(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// alpine-virt is a real catalog ID; if this reached iso.Resolve or
	// iso.Download it would try the network and this test would hang or
	// fail offline. It must not get that far.
	err := DownloadImage(ctx, "alpine-virt", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestMatchLocalDistinguishesAlpineFlavours pins that alpine-standard and
// alpine-virt are not interchangeable.
//
// iso.Infer reports only an OS and a backend, and the two entries share both,
// so the Infer fallback matched whichever alpine .iso was on disk for BOTH.
// `stoat images` reported alpine-virt as downloaded with only the standard ISO
// present, and `create --image alpine-virt` silently built on the standard
// image, a 352 MiB general-purpose kernel where the user asked for the 66 MiB
// virtualised one, with no error to notice.
func TestMatchLocalDistinguishesAlpineFlavours(t *testing.T) {
	var std, virt iso.Entry
	for _, e := range iso.Catalog() {
		switch e.ID {
		case "alpine-standard":
			std = e
		case "alpine-virt":
			virt = e
		}
	}
	if std.Flavor == "" || virt.Flavor == "" {
		t.Fatal("expected both alpine entries to carry a Flavor")
	}

	// Only the STANDARD image is present.
	files := []string{"alpine-standard-3.24.1-x86_64.iso"}
	if got := MatchLocal(std, files); got != files[0] {
		t.Errorf("alpine-standard did not match its own image: %q", got)
	}
	if got := MatchLocal(virt, files); got != "" {
		t.Errorf("alpine-virt matched %q, which is the STANDARD image", got)
	}

	// Only the VIRT image is present: the mirror image of the same rule.
	files = []string{"alpine-virt-3.24.1-x86_64.iso"}
	if got := MatchLocal(virt, files); got != files[0] {
		t.Errorf("alpine-virt did not match its own image: %q", got)
	}
	if got := MatchLocal(std, files); got != "" {
		t.Errorf("alpine-standard matched %q, which is the VIRT image", got)
	}

	// Both present: each takes its own.
	files = []string{"alpine-standard-3.24.1-x86_64.iso", "alpine-virt-3.24.1-x86_64.iso"}
	if got := MatchLocal(std, files); got != files[0] {
		t.Errorf("alpine-standard = %q", got)
	}
	if got := MatchLocal(virt, files); got != files[1] {
		t.Errorf("alpine-virt = %q", got)
	}
}
