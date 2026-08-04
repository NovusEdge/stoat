package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// drainScan collects every batch the scanner emits, so a test can assert on
// the whole result set without caring how it was chunked.
func drainScan(ch <-chan []foundImage) []foundImage {
	var all []foundImage
	for batch := range ch {
		all = append(all, batch...)
	}
	return all
}

func paths(found []foundImage) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.path)
	}
	return out
}

func has(found []foundImage, suffix string) bool {
	for _, p := range paths(found) {
		if strings.HasSuffix(p, suffix) {
			return true
		}
	}
	return false
}

// The scanner exists to find disk images and nothing else: a home directory is
// mostly files we must not offer, and walking into hidden trees (.cache, .git,
// node_modules under a dotted dir) costs far more time than everything else
// combined while finding nothing.
func TestScanImagesFindsOnlyImagesOutsideHiddenDirs(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("downloads/alpine.iso")
	write("vms/disk.qcow2")
	write("vms/raw.img")
	write("notes.txt")
	write("downloads/archive.tar.gz")
	write(".cache/stale.iso")
	write(".local/share/deep/hidden.qcow2")

	found := drainScan(scanImages(root, nil))

	for _, want := range []string{"alpine.iso", "disk.qcow2", "raw.img"} {
		if !has(found, want) {
			t.Errorf("scan missed %s; found %v", want, paths(found))
		}
	}
	for _, unwanted := range []string{"notes.txt", "archive.tar.gz"} {
		if has(found, unwanted) {
			t.Errorf("scan offered %s, which is not a disk image", unwanted)
		}
	}
	for _, hidden := range []string{"stale.iso", "hidden.qcow2"} {
		if has(found, hidden) {
			t.Errorf("scan descended into a hidden directory and found %s", hidden)
		}
	}
}

// A file's size is shown next to it, so a scan that reports zero for
// everything would render a column of "0 B", and stat failures must not
// abort the walk.
func TestScanImagesReportsSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "big.iso"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	found := drainScan(scanImages(root, nil))
	if len(found) != 1 {
		t.Fatalf("want exactly one image, got %v", paths(found))
	}
	if found[0].size != 4096 {
		t.Errorf("size = %d, want 4096", found[0].size)
	}
}

// An unreadable directory is normal in a home directory and must not end the
// walk, or everything after it would silently go missing.
func TestScanImagesSurvivesAnUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 directory")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })
	if err := os.WriteFile(filepath.Join(root, "after.iso"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if found := drainScan(scanImages(root, nil)); !has(found, "after.iso") {
		t.Errorf("an unreadable directory ended the walk; found %v", paths(found))
	}
}

// If nothing keeps reading (the finder is left, or the modal closes on a
// selection), the walk must still stop, otherwise the goroutine blocks
// forever on the 5th pending batch (out has capacity 4) and leaks, holding an
// open WalkDir, once per abandoned scan.
func TestScanImagesStopsWhenCancelled(t *testing.T) {
	root := t.TempDir()
	// More than fits in the channel's buffer (4 batches) plus one more, so
	// the goroutine is guaranteed to still be producing, and blocked on a
	// send, when cancel fires.
	for i := 0; i < 200; i++ {
		p := filepath.Join(root, fmt.Sprintf("img%03d.iso", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cancel := make(chan struct{})
	ch := scanImages(root, cancel)
	<-ch // one batch read, buffer fills behind it, goroutine blocks on the next send
	close(cancel)

	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the scan goroutine did not stop after cancel; it is blocked forever on a send")
	}
}

// The channel must close, or waitForImages blocks forever and the modal
// never learns the scan is finished.
func TestScanImagesClosesItsChannel(t *testing.T) {
	ch := scanImages(t.TempDir(), nil)
	drainScan(ch)
	if _, open := <-ch; open {
		t.Error("the scan channel is still open after the walk finished")
	}
}
