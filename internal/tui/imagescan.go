package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// imageExts is what counts as a disk image. Same set byoOptionFromPath
// resolves, kept in one place so the two entry points cannot drift into
// offering different files.
var imageExts = []string{".iso", ".qcow2", ".img"}

// scanBatch is how many files pile up before a batch is sent. One message
// per file would rebuild the list's filter once per file. One message at
// the end would leave the pane empty for the whole walk. Batching shows
// results early without a message storm.
const scanBatch = 32

// foundImage is one candidate. Only what the row needs: the path is both the
// label and the value, and the size is the one detail that distinguishes two
// same-named files.
type foundImage struct {
	path string
	size int64
}

// scanImages walks root in the background and streams batches of disk
// images down the returned channel. The channel closes when the walk
// finishes. The walk runs in a goroutine because a home directory can take
// seconds. The caller draws each batch as it lands, instead of waiting for
// all of them.
//
// scanImages uses WalkDir, not Walk. WalkDir reads directory entries in
// bulk and skips one Lstat per file. It also does not follow symlinks,
// which keeps a symlink loop from hanging the scan.
//
// cancel, when closed, ends the walk at the next batch boundary. Without
// it, a caller who stops reading, because the finder is left or the modal
// closes on a selection, leaves the goroutine parked forever on an
// unconsumed send. This happens once four batches (128 images) fill the
// buffer: out has capacity 4, and a bare send then blocks. A nil cancel is
// fine. The send then has no second case to race against.
func scanImages(root string, cancel <-chan struct{}) <-chan []foundImage {
	out := make(chan []foundImage, 4)

	go func() {
		defer close(out)
		batch := make([]foundImage, 0, scanBatch)
		// flush reports whether the batch was delivered. false means cancel
		// fired first and the walk must stop. Returning fs.SkipAll from the
		// WalkDirFunc is what actually ends filepath.WalkDir.
		flush := func() bool {
			if len(batch) == 0 {
				return true
			}
			select {
			case out <- batch:
				batch = make([]foundImage, 0, scanBatch)
				return true
			case <-cancel:
				return false
			}
		}

		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				// An unreadable directory is ordinary in a home directory.
				// Returning the error would abort the whole walk and silently
				// drop everything after it, so skip just this entry.
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				// Hidden trees hold most of a home directory's file count
				// (.cache, .git, .local) and hold no disk images. Skipping
				// them is most of why this scan finishes quickly. root
				// itself is never skipped, even when it is a dotted path.
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if !hasImageExt(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				// Deleted between the directory read and the stat. Offering a
				// file that is already gone is worse than not offering it.
				return nil
			}
			batch = append(batch, foundImage{path: path, size: info.Size()})
			if len(batch) >= scanBatch {
				if !flush() {
					return fs.SkipAll
				}
			}
			return nil
		})
		flush()
	}()

	return out
}

func hasImageExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, want := range imageExts {
		if ext == want {
			return true
		}
	}
	return false
}

// imagesFoundMsg carries one batch. done is set once the channel closes.
// This is how the modal knows to stop saying it is still looking.
//
// gen is the scan generation waitForImages was issued for. The modal
// stamps its current generation into every waitForImages call, and drops
// any message whose gen does not match. Without this check, a message
// parked on an abandoned scan, from esc or re-entering the finder, would
// deliver against the new scan instead, doubling its results or ending it
// early.
type imagesFoundMsg struct {
	batch []foundImage
	done  bool
	gen   int
}

// waitForImages reads ONE batch and returns it as a message. The receiver
// re-issues it until done, which is the standard way to pump a channel into
// Bubbletea: a command that blocks forever would never let the UI redraw.
func waitForImages(ch <-chan []foundImage, gen int) tea.Cmd {
	return func() tea.Msg {
		batch, ok := <-ch
		return imagesFoundMsg{batch: batch, done: !ok, gen: gen}
	}
}

// homeDir is the scan root. A failure here is not worth an error path, since the
// current directory is a reasonable place to look for a disk image, and the
// tree browser is still there for anywhere else.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
