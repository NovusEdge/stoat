package iso

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/guest"
)

// TestDownload_RenameFailureCleansUpPart drives Download down the success
// path (matching checksum) but forces the final os.Rename to fail by
// pre-creating a non-empty directory at the destination path. It asserts
// that the .part file is removed rather than orphaned: every error path in
// Download must clean up .part.
//
// This test is hermetic: it never touches the network or the real Alpine
// mirror. A local httptest.Server serves the "download", and the expected
// checksum is computed from the bytes that server serves.
func TestDownload_RenameFailureCleansUpPart(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for an alpine iso")
	sum := sha256.Sum256(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	origMirror := downloadMirror
	downloadMirror = srv.URL + "/"
	defer func() { downloadMirror = origMirror }()

	r := &Release{
		Flavor:  "alpine-standard",
		File:    "fake.iso",
		Version: "0.0.0",
		SHA256:  hex.EncodeToString(sum[:]),
	}

	dir := filepath.Join(os.Getenv("STOAT_HOME"), "isos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, r.File)
	part := final + ".part"

	// Make the final rename target a non-empty directory so os.Rename(part,
	// final) fails with ENOTEMPTY/EEXIST on Linux.
	if err := os.Mkdir(final, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(final, "occupied"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Download(context.Background(), r, nil)
	if err == nil {
		t.Fatal("expected Download to fail because final is a non-empty directory")
	}

	if _, statErr := os.Stat(part); !os.IsNotExist(statErr) {
		t.Fatalf(".part file was left behind after rename failure: %v", statErr)
	}
}

// TestCatalog_EntriesValid checks the curated catalog's shape: every entry
// must be identifiable (ID, OS) and fetchable (URL), and must declare a
// backend the rest of stoat knows how to provision with.
func TestCatalog_EntriesValid(t *testing.T) {
	validBackend := map[string]bool{"apkovl": true, "cloudinit": true, "ssh": true}

	entries := Catalog()
	if len(entries) == 0 {
		t.Fatal("Catalog() returned no entries")
	}
	for _, e := range entries {
		if e.ID == "" {
			t.Errorf("entry with empty ID (os=%q)", e.OS)
		}
		if e.OS == "" {
			t.Errorf("entry %q: empty OS", e.ID)
		}
		if e.URL == "" {
			t.Errorf("entry %q: empty URL", e.ID)
		}
		if !validBackend[e.Backend] {
			t.Errorf("entry %q: invalid backend %q", e.ID, e.Backend)
		}
		// Every entry needs a variant label: it is the only thing telling
		// two entries of the same OS apart in a grouped picker, and an
		// entry that forgets it renders as a blank row.
		if e.Variant == "" {
			t.Errorf("entry %q: empty Variant", e.ID)
		}
	}
}

// TestByOSGroupsWithoutLosingEntries covers the grouped view the image
// picker reads. The failure that matters is silent: a grouping that drops or
// duplicates an entry still renders a perfectly plausible menu, just one
// missing an image nobody can then select.
func TestByOSGroupsWithoutLosingEntries(t *testing.T) {
	groups := ByOS()
	if len(groups) == 0 {
		t.Fatal("ByOS() returned no groups")
	}

	// Every catalog entry appears exactly once across all groups, under its
	// own OS.
	seen := map[string]int{}
	for _, g := range groups {
		if g.OS == "" {
			t.Error("group with empty OS")
		}
		if len(g.Entries) == 0 {
			t.Errorf("group %q has no entries", g.OS)
		}
		for _, e := range g.Entries {
			if e.OS != g.OS {
				t.Errorf("entry %q (os %q) filed under group %q", e.ID, e.OS, g.OS)
			}
			seen[e.ID]++
		}
	}
	for _, e := range Catalog() {
		if seen[e.ID] != 1 {
			t.Errorf("entry %q appears %d times in ByOS(), want exactly 1", e.ID, seen[e.ID])
		}
	}
	if len(seen) != len(Catalog()) {
		t.Errorf("ByOS() covers %d entries, catalog has %d", len(seen), len(Catalog()))
	}

	// One group per distinct OS, not one per entry. Counted from the
	// catalog rather than hardcoded: asserting "entries minus one" would
	// encode today's accident that exactly one OS has two variants, and
	// would fail for the wrong reason the day a second one does.
	distinct := map[string]bool{}
	for _, e := range Catalog() {
		distinct[e.OS] = true
	}
	if len(groups) != len(distinct) {
		t.Errorf("got %d groups, want %d (one per distinct OS)", len(groups), len(distinct))
	}

	// Groups follow catalog order rather than being sorted.
	var wantOrder []string
	for _, e := range Catalog() {
		if len(wantOrder) == 0 || wantOrder[len(wantOrder)-1] != e.OS {
			wantOrder = append(wantOrder, e.OS)
		}
	}
	for i, g := range groups {
		if g.OS != wantOrder[i] {
			t.Errorf("group %d is %q, want %q: ByOS must preserve catalog order", i, g.OS, wantOrder[i])
		}
	}

	// Variants within a group must be distinct, or the second level of the
	// picker shows two rows reading the same thing.
	for _, g := range groups {
		vs := map[string]bool{}
		for _, e := range g.Entries {
			if vs[e.Variant] {
				t.Errorf("group %q has two entries labelled %q", g.OS, e.Variant)
			}
			vs[e.Variant] = true
		}
	}
}

// TestInfer covers the BYO filename heuristic's outcome classes: a
// recognisable cloud qcow2/img (any registered OS), a recognisable Alpine
// live ISO, a bare cloud image with no OS hint at all, and an unrecognised
// name that must fall back to ssh rather than guess wrong.
func TestInfer(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		wantBackend string
		wantOS      string
	}{
		// Infer recognises every registered OS by its FilenameHints, not
		// just Alpine; see TestInferRecognisesEveryRegisteredOS. A BYO
		// ubuntu/debian cloud image gets whatever recipes its OS supports
		// instead of zero (recipes.List("", backend) with an empty OS).
		{"ubuntu cloudimg qcow2", "ubuntu-24.04-server-cloudimg-amd64.qcow2", "cloudinit", "ubuntu"},
		{"debian genericcloud qcow2", "debian-12-genericcloud-amd64.qcow2", "cloudinit", "debian"},
		{"bare .img", "some-cloud-image.img", "cloudinit", ""},
		{"alpine iso", "alpine-standard-3.20.0-x86_64.iso", "apkovl", "alpine"},
		// The alpine-cloud catalog entry downloads exactly this filename.
		// An empty OS here would send the BYO fallback in
		// internal/tui/form.go to guestShell(""), which returns /bin/bash,
		// and Alpine has no bash.
		{"alpine cloud qcow2", "generic_alpine-3.24.1-x86_64-bios-cloudinit-r0.qcow2", "cloudinit", "alpine"},
		{"random unrecognised name", "my-random-install.raw", "ssh", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			backend, os := Infer(c.file)
			if backend != c.wantBackend || os != c.wantOS {
				t.Errorf("Infer(%q) = (%q, %q), want (%q, %q)", c.file, backend, os, c.wantBackend, c.wantOS)
			}
		})
	}
}

// Every OS in the registry must be recognisable from a filename, for both an
// iso and a qcow2. An OS that cannot be inferred produces an EMPTY OS on a BYO
// image, which is how the seed ends up asking for a shell the guest lacks.
func TestInferRecognisesEveryRegisteredOS(t *testing.T) {
	for _, o := range guest.All() {
		for _, ext := range []string{".iso", ".qcow2", ".img"} {
			name := o.Name + "-test-image" + ext
			_, gotOS := Infer(name)
			if gotOS != o.Name {
				t.Errorf("Infer(%q) os = %q, want %q", name, gotOS, o.Name)
			}
		}
	}
}

// TestResolveAndDownload_Entry drives the generalized catalog-Entry path
// end to end against two local httptest servers (one serving the "image"
// bytes, one serving a SHA256SUMS-style checksum file) and asserts Download
// only renames .part into place once the fetched bytes match the checksum
// parsed out of that sums file. No network access; the checksum is
// verified, not assumed.
func TestResolveAndDownload_Entry(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for a cloud qcow2 image")
	sum := sha256.Sum256(body)
	const filename = "example-cloudimg-amd64.qcow2"

	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write(body)
	}))
	defer fileSrv.Close()

	sumsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  %s\nfeedface  some-other-file.qcow2\n", hex.EncodeToString(sum[:]), filename)
	}))
	defer sumsSrv.Close()

	entry := Entry{
		ID:          "test-entry",
		OS:          "testos",
		Backend:     "cloudinit",
		URL:         fileSrv.URL + "/" + filename,
		ChecksumURL: sumsSrv.URL + "/SHA256SUMS",
		SSHUser:     "stoat",
	}

	r, err := Resolve(entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.File != filename {
		t.Fatalf("Resolve File = %q, want %q", r.File, filename)
	}
	wantSum := hex.EncodeToString(sum[:])
	if r.SHA256 != wantSum {
		t.Fatalf("Resolve SHA256 = %q, want %q", r.SHA256, wantSum)
	}

	rel, err := Download(context.Background(), r, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if rel != filepath.Join("isos", filename) {
		t.Fatalf("Download rel path = %q", rel)
	}
	if !r.Verified {
		t.Fatal("Download did not set Verified after a matching checksum")
	}

	final := filepath.Join(os.Getenv("STOAT_HOME"), "isos", filename)
	if _, err := os.Stat(final); err != nil {
		t.Fatalf("expected final file to exist: %v", err)
	}
	if _, err := os.Stat(final + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part left behind after successful download")
	}

	// Now with a checksum that won't match: Download must refuse to rename.
	bad := &Release{File: "mismatch.qcow2", URL: fileSrv.URL + "/" + filename, SHA256: "deadbeef"}
	if _, err := Download(context.Background(), bad, nil); err == nil {
		t.Fatal("expected Download to fail on checksum mismatch")
	}
	badFinal := filepath.Join(os.Getenv("STOAT_HOME"), "isos", "mismatch.qcow2")
	if _, err := os.Stat(badFinal); !os.IsNotExist(err) {
		t.Fatalf("mismatched download was renamed into place")
	}
	if _, err := os.Stat(badFinal + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part left behind after checksum mismatch")
	}
}

// TestDownload_SHA512 exercises the algorithm-agnostic verification path: a
// 128-hex expected digest (as Debian's SHA512SUMS publishes) must be
// checked with sha512, not sha256, and must still gate the .part rename on
// a match.
func TestDownload_SHA512(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for a debian genericcloud image")
	sum := sha512.Sum512(body)
	const filename = "example-genericcloud-amd64.qcow2"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := &Release{File: filename, URL: srv.URL + "/" + filename, SHA256: hex.EncodeToString(sum[:])}
	if len(r.SHA256) != 128 {
		t.Fatalf("test setup: expected a 128-hex sha512 digest, got %d chars", len(r.SHA256))
	}

	rel, err := Download(context.Background(), r, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if rel != filepath.Join("isos", filename) {
		t.Fatalf("Download rel path = %q", rel)
	}
	if !r.Verified {
		t.Fatal("Download did not set Verified for a matching sha512 digest")
	}
}

// TestDownload_UnverifiedWhenNoChecksum: an entry resolved with no checksum
// available must still download, never silently fail, but Verified must
// stay false so a caller can distinguish "downloaded, unverified" from
// "downloaded, verified".
func TestDownload_UnverifiedWhenNoChecksum(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("no checksum was published for this one")
	const filename = "unverified-cloudimg-amd64.qcow2"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	r := &Release{File: filename, URL: srv.URL + "/" + filename}

	if _, err := Download(context.Background(), r, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if r.Verified {
		t.Fatal("Download set Verified with no checksum to verify against")
	}
}

// TestParseChecksum_BSDFormat covers Fedora's CHECKSUM format ("SHA256
// (filename) = hex"), including the surrounding PGP clearsign armor lines
// that must be skipped rather than misparsed.
func TestParseChecksum_BSDFormat(t *testing.T) {
	const filename = "Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2"
	want := "846574c8a97cd2d8dc1f231062d73107cc85cbbbda56335e264a46e3a6c8ab20"
	body := []byte(`-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA256

# Fedora-Cloud-Base-AmazonEC2-43-1.6.x86_64.raw.xz: 592568816 bytes
SHA256 (Fedora-Cloud-Base-AmazonEC2-43-1.6.x86_64.raw.xz) = 91a3fa8f5fd870ca4de56c24df97ceb4f8bd806c2e01a1a8a4fa780395a84bf
# ` + filename + `: 583335936 bytes
SHA256 (` + filename + `) = ` + want + `
-----BEGIN PGP SIGNATURE-----

iQIzBAEBCAAdFiEExufwgc+A4TFGZ26IgptgZjFkVTEFAmj9DpIACgkQgptgZjFk
-----END PGP SIGNATURE-----
`)

	got, err := parseChecksum(body, filename)
	if err != nil {
		t.Fatalf("parseChecksum: %v", err)
	}
	if got != want {
		t.Fatalf("parseChecksum = %q, want %q", got, want)
	}
}

// TestCatalog_ArchAndDebianHaveChecksumURL: Arch publishes a matching
// .SHA256 sidecar and Debian publishes SHA512SUMS, so both entries must
// carry a ChecksumURL rather than shipping unverified for no reason.
func TestCatalog_ArchAndDebianHaveChecksumURL(t *testing.T) {
	for _, id := range []string{"arch-cloud", "debian-13"} {
		found := false
		for _, e := range Catalog() {
			if e.ID == id {
				found = true
				if e.ChecksumURL == "" {
					t.Errorf("entry %q: expected a non-empty ChecksumURL", id)
				}
			}
		}
		if !found {
			t.Errorf("catalog entry %q not found", id)
		}
	}
}

// TestCatalog_FedoraURLNotArchived guards against a Fedora catalog URL
// pointing at a release that has aged out of releases/ into
// archives.fedoraproject.org, a 404 for users.
//
// Grepping e.URL for the literal string "archives.fedoraproject.org" can
// never fail: the catalog URL is always spelled with releases/ in it, right
// up until the day Fedora retires that release and it starts 404ing.
// Catching that means asking the server, so this does a ranged GET against
// the real URL. Following this package's convention of skipping rather than
// failing when a dependency is not there (see the xorriso/qemu-img skips
// elsewhere in this repo), a network-level failure (DNS, connect, TLS)
// skips instead of failing: only a reachable server reporting the pinned
// release gone is a real failure.
func TestCatalog_FedoraURLNotArchived(t *testing.T) {
	// Every other test in this package serves from a local httptest server;
	// this is the only one that touches the real internet, so -short stays
	// fully offline.
	if testing.Short() {
		t.Skip("live network check; skipped under -short")
	}
	var e Entry
	for _, c := range Catalog() {
		if c.ID == "fedora-cloud" {
			e = c
		}
	}
	if e.ID == "" {
		t.Fatal(`catalog entry "fedora-cloud" not found`)
	}

	req, err := http.NewRequest(http.MethodGet, e.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("network unreachable, skipping live fedora-cloud check: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return
	case http.StatusNotFound, http.StatusGone:
		// The one thing this test exists to catch: the pinned release has
		// left releases/ for the archive, and every user's download 404s.
		t.Errorf("fedora-cloud pinned release is gone from %s: %s (final URL %s); bump the release and both compose suffixes",
			e.URL, resp.Status, resp.Request.URL)
	default:
		// A mirror having a bad day (5xx, 429, a geo-redirect to a sick
		// host) is not the catalog being wrong. Failing on it would make
		// the whole suite red for a reason no commit can fix, which is how
		// CI here has gone quietly red before.
		t.Skipf("fedora mirror returned %s; not a catalog defect, skipping", resp.Status)
	}
}

// TestDownloadOutlastsMetadataTimeout: Download must not share the metadata
// client's Timeout, which bounds the whole request including the body. Any
// image taking longer than that ceiling would die mid-transfer, which is
// every real image. The server here dribbles the body out over noticeably
// longer than the metadata client's own timeout; the download must still
// complete.
func TestDownloadOutlastsMetadataTimeout(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	// Shrink the metadata client for the length of the test, so "longer than
	// the metadata timeout" costs milliseconds instead of 30 real seconds.
	origClient := client
	client = &http.Client{Timeout: 50 * time.Millisecond}
	defer func() { client = origClient }()

	body := []byte(strings.Repeat("payload!", 64))
	sum := sha256.Sum256(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		flusher, _ := w.(http.Flusher)
		for i := 0; i < len(body); i += 64 {
			_, _ = w.Write(body[i:min(i+64, len(body))])
			if flusher != nil {
				flusher.Flush()
			}
			// Total transfer time lands well past the metadata timeout.
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer srv.Close()

	origMirror := downloadMirror
	downloadMirror = srv.URL + "/"
	defer func() { downloadMirror = origMirror }()

	r := &Release{File: "slow.iso", Version: "0.0.0", SHA256: hex.EncodeToString(sum[:])}

	var lastDone, lastTotal int64
	got, err := Download(context.Background(), r, func(done, total int64) { lastDone, lastTotal = done, total })
	if err != nil {
		t.Fatalf("slow download failed: %v", err)
	}
	if got != filepath.Join("isos", "slow.iso") {
		t.Errorf("Download returned %q", got)
	}
	if lastDone != int64(len(body)) {
		t.Errorf("progress reported %d bytes, want %d", lastDone, len(body))
	}
	if lastTotal != int64(len(body)) {
		t.Errorf("progress reported total %d, want %d", lastTotal, len(body))
	}
	if !r.Verified {
		t.Error("checksum should have verified")
	}
}

// TestResolve_AlpineDirectURLEntry guards against gating Resolve's
// index-vs-direct-URL choice on OS == "alpine": alpine-cloud is an alpine
// entry with a direct URL and no Flavor, so a bare OS check would send it
// through Latest("") and fail to find any release in the index, even though
// the entry itself is perfectly resolvable as a direct URL.
func TestResolve_AlpineDirectURLEntry(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for an alpine cloud qcow2")
	const filename = "alpine-cloud.qcow2"

	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write(body)
	}))
	defer fileSrv.Close()

	entry := Entry{
		ID:      "alpine-cloud",
		OS:      "alpine",
		Backend: "cloudinit",
		URL:     fileSrv.URL + "/" + filename,
		SSHUser: "stoat",
	}

	r, err := Resolve(entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.File != filename {
		t.Fatalf("Resolve File = %q, want %q", r.File, filename)
	}
	if r.URL != entry.URL {
		t.Fatalf("Resolve URL = %q, want %q (direct-URL path, not the flavor index)", r.URL, entry.URL)
	}
}

// Alpine belongs in the catalog as a CLOUD image, not only as an ISO. It is
// the one distro stoat offers in both shapes, and the cloud one is the only
// path to a persistent Alpine VM that needs no manual install.
func TestCatalogOffersAlpineCloud(t *testing.T) {
	var got *Entry
	for i, e := range Catalog() {
		if e.OS == "alpine" && e.Backend == "cloudinit" {
			got = &Catalog()[i]
		}
	}
	if got == nil {
		t.Fatal("no alpine cloudinit entry in the catalog")
	}
	if !strings.HasSuffix(got.URL, ".qcow2") {
		t.Errorf("URL is not a qcow2: %s", got.URL)
	}
	// The tiny-cloud flavor has no sudo key, no cloud-init status and no
	// plaintext password support: everything stoat's seed and its polling
	// depend on. Only the cloudinit flavor works.
	if !strings.Contains(got.URL, "cloudinit") {
		t.Errorf("must be the cloudinit flavor, got %s", got.URL)
	}
	if strings.Contains(got.URL, "-uefi-") {
		t.Errorf("bios flavor only: stoat boots SeaBIOS, not OVMF: %s", got.URL)
	}
	if got.SSHUser != "stoat" {
		t.Errorf("SSHUser = %q, want stoat (the seed creates that account)", got.SSHUser)
	}
}

// TestDownloadConcurrentSameTargetDoesNotCorrupt guards against a silent
// data-corruption bug: os.Create on final+".part" truncates in place and
// grants no exclusivity, so two downloads of the same image held two
// handles on the same inode. Each wrote at independent offsets; a writer
// still going when the other renamed its .part into place kept writing
// straight into the finished image.
//
// The checksum could not catch it: the digest is fed from the bytes each
// goroutine reads off the network, never re-read from disk. Whichever
// writer finished computed a valid digest of its own complete stream, set
// Verified, and renamed a mangled file into place as a verified image.
//
// The two downloads must serve different bytes for this to be detectable:
// interleaving identical content is invisible. Cloud images are rebuilt in
// place (see Catalog()), so cancelling a download and retrying can fetch
// different content under one filename. Each download gets its own temp
// file now, so whichever wins must land byte-for-byte as one server
// response, never a mix.
func TestDownloadConcurrentSameTargetDoesNotCorrupt(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	const size = 512 * 1024
	var nth atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every request gets a DIFFERENT filler byte, so any interleaving of
		// two responses shows up as a file that is not uniformly one letter.
		fill := byte('A') + byte(nth.Add(1)-1)
		chunk := bytes.Repeat([]byte{fill}, 4096)
		for off := 0; off < size; off += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(time.Millisecond)
		}
	}))
	defer srv.Close()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// No checksum, exactly like the three alpine catalog entries: those
			// have no digest backstop at all, so the file itself is the only
			// thing standing between the user and a corrupt image.
			r := &Release{File: "same.qcow2", URL: srv.URL}
			Download(context.Background(), r, nil) //nolint:errcheck // one may lose the rename race; the CONTENT is what is under test
		}()
	}
	wg.Wait()

	final := filepath.Join(os.Getenv("STOAT_HOME"), "isos", "same.qcow2")
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatalf("no image landed: %v", err)
	}
	if len(got) != size {
		t.Fatalf("final image is %d bytes, want %d: writers interleaved or truncated each other", len(got), size)
	}
	for i, b := range got {
		if b != got[0] {
			t.Fatalf("final image is a MIX of two downloads: byte 0 is %q but byte %d is %q", got[0], i, b)
		}
	}

	entries, err := os.ReadDir(filepath.Join(os.Getenv("STOAT_HOME"), "isos"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			t.Errorf("leftover partial file %q", e.Name())
		}
	}
}

// TestDownloadCancelStopsTheTransfer pins that cancelling ctx actually stops
// the read rather than merely returning control to the caller. Before
// Download took a ctx, abandoning a download (the TUI's esc) left the
// goroutine reading the socket until the 60s stall timer fired, which is the
// only reason that timer exists.
func TestDownloadCancelStopsTheTransfer(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	served := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never finishes: keeps dribbling until the client goes away.
		for i := 0; i < 100000; i++ {
			if _, err := w.Write(bytes.Repeat([]byte{'B'}, 4096)); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			once.Do(func() { close(served) })
			time.Sleep(time.Millisecond)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		r := &Release{File: "endless.qcow2", URL: srv.URL}
		_, err := Download(ctx, r, nil)
		done <- err
	}()

	<-served // the transfer is genuinely in flight
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Download returned success after cancellation")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Download did not return within 10s of cancel: ctx is not reaching resp.Body.Read")
	}

	// A cancelled transfer must leave nothing usable behind.
	final := filepath.Join(os.Getenv("STOAT_HOME"), "isos", "endless.qcow2")
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Error("a cancelled download was renamed into place")
	}
}
