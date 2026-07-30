package iso

import (
	"crypto/sha256"
	"encoding/hex"
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
