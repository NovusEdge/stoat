package iso

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
