package iso

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDownload_RenameFailureCleansUpPart drives Download down the success
// path (matching checksum) but forces the final os.Rename to fail by
// pre-creating a non-empty directory at the destination path. It asserts
// that the .part file is removed rather than orphaned, per the brief's
// invariant that every error path in Download cleans up .part.
//
// This test is hermetic: it never touches the network or the real Alpine
// mirror. The "download" is served by a local httptest.Server, and the
// expected checksum is computed from the bytes that server serves.
func TestDownload_RenameFailureCleansUpPart(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for an alpine iso")
	sum := sha256.Sum256(body)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(body)
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

	_, err := Download(r, nil)
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
	}
}

// TestInfer covers the BYO filename heuristic's three outcome classes:
// a recognisable cloud qcow2/img, a recognisable Alpine live ISO, and an
// unrecognised name that must fall back to ssh rather than guess wrong.
func TestInfer(t *testing.T) {
	cases := []struct {
		name        string
		file        string
		wantBackend string
		wantOS      string
	}{
		{"ubuntu cloudimg qcow2", "ubuntu-24.04-server-cloudimg-amd64.qcow2", "cloudinit", ""},
		{"debian genericcloud qcow2", "debian-12-genericcloud-amd64.qcow2", "cloudinit", ""},
		{"bare .img", "some-cloud-image.img", "cloudinit", ""},
		{"alpine iso", "alpine-standard-3.20.0-x86_64.iso", "apkovl", "alpine"},
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
		w.Write(body)
	}))
	defer fileSrv.Close()

	sumsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "%s  %s\nfeedface  some-other-file.qcow2\n", hex.EncodeToString(sum[:]), filename)
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

	rel, err := Download(r, nil)
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
	if _, err := Download(bad, nil); err == nil {
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
// a match. Regression coverage for the fix that made Download pick the hash
// by digest length instead of hardcoding sha256.
func TestDownload_SHA512(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("hello, this stands in for a debian genericcloud image")
	sum := sha512.Sum512(body)
	const filename = "example-genericcloud-amd64.qcow2"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	r := &Release{File: filename, URL: srv.URL + "/" + filename, SHA256: hex.EncodeToString(sum[:])}
	if len(r.SHA256) != 128 {
		t.Fatalf("test setup: expected a 128-hex sha512 digest, got %d chars", len(r.SHA256))
	}

	rel, err := Download(r, nil)
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

// TestDownload_UnverifiedWhenNoChecksum is the Important-3 regression: an
// entry resolved with no checksum available must still download (never
// silently fail), but Verified must stay false so a caller can distinguish
// "downloaded, unverified" from "downloaded, verified" instead of the two
// looking identical.
func TestDownload_UnverifiedWhenNoChecksum(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())

	body := []byte("no checksum was published for this one")
	const filename = "unverified-cloudimg-amd64.qcow2"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	r := &Release{File: filename, URL: srv.URL + "/" + filename}

	if _, err := Download(r, nil); err != nil {
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

// TestCatalog_ArchAndDebianHaveChecksumURL is a narrow regression for the
// review findings: Arch does publish a matching .SHA256 sidecar and Debian
// does publish SHA512SUMS, so both entries must carry a ChecksumURL now
// rather than shipping unverified for no real reason.
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

// TestCatalog_FedoraURLNotArchived guards against the exact defect the
// review found: a Fedora catalog URL pointing at a release that has aged
// out of releases/ into archives.fedoraproject.org (a 404 for users today).
//
// Grepping e.URL for the literal string "archives.fedoraproject.org" (the
// old version of this test) can never fail: the catalog URL is always
// spelled with releases/ in it, right up until the day Fedora retires that
// release and it starts 404ing. Catching that means actually asking the
// server, so this does a ranged GET against the real URL. Per this
// package's convention of skipping rather than failing when a dependency
// isn't there (see the xorriso/qemu-img skips elsewhere in this repo), a
// network-level failure (DNS, connect, TLS) skips instead of failing —
// only a reachable server telling us the pinned release is gone is a real
// failure.
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
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return
	case http.StatusNotFound, http.StatusGone:
		// The one thing this test exists to catch: the pinned release has
		// left releases/ for the archive, and every user's download 404s.
		t.Errorf("fedora-cloud pinned release is gone from %s: %s (final URL %s) — bump the release and both compose suffixes",
			e.URL, resp.Status, resp.Request.URL)
	default:
		// A mirror having a bad day (5xx, 429, a geo-redirect to a sick
		// host) is not the catalog being wrong. Failing on it would make
		// the whole suite red for a reason no commit can fix, which is how
		// CI here has gone quietly red before.
		t.Skipf("fedora mirror returned %s; not a catalog defect, skipping", resp.Status)
	}
}

// TestDownloadOutlastsMetadataTimeout is the regression test for a reported
// failure: "ubuntu: context deadline exceeded (Client.Timeout or context
// cancellation while reading body)". Download shared the metadata client,
// whose Timeout bounds the WHOLE request including the body — so any image
// taking longer than that ceiling died mid-transfer, which is every real
// image. The server here dribbles the body out over noticeably longer than
// the metadata client's own timeout; the download must still complete.
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
			w.Write(body[i:min(i+64, len(body))])
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
	got, err := Download(r, func(done, total int64) { lastDone, lastTotal = done, total })
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
