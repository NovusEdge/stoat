package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// imageExts is what counts as a disk image. Same set the tree browser
// already allows (imagemodal.go's openBrowser), kept in one place so the two
// entry points cannot drift into offering different files.
var imageExts = []string{".iso", ".qcow2", ".img"}

// scanBatch is how many files pile up before a batch is sent. One message per
// file would be correct but makes the list rebuild its filter once per file;
// one message at the end would mean staring at an empty pane for the whole
// walk. Batching gets results on screen early without a message storm.
const scanBatch = 32

// foundImage is one candidate. Only what the row needs: the path is both the
// label and the value, and the size is the one detail that distinguishes two
// same-named files.
type foundImage struct {
	path string
	size int64
}

// scanImages walks root in the background and streams batches of disk images
// down the returned channel, which is CLOSED when the walk finishes. The walk
// runs in a goroutine because a home directory can take seconds; the caller
// draws each batch as it lands rather than waiting for all of them.
//
// WalkDir, not Walk: it reads directory entries in bulk and skips one Lstat
// per file. It also does not follow symlinks, which is what keeps a symlink
// loop from hanging the scan.
func scanImages(root string) <-chan []foundImage {
	out := make(chan []foundImage, 4)

	go func() {
		defer close(out)
		batch := make([]foundImage, 0, scanBatch)
		flush := func() {
			if len(batch) == 0 {
				return
			}
			out <- batch
			batch = make([]foundImage, 0, scanBatch)
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
				// Hidden trees are where the file count lives (.cache, .git,
				// .local) and where disk images do not. Skipping them is most
				// of the reason this finishes quickly. root itself is never
				// skipped, even when it is a dotted path.
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
				flush()
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

// imagesFoundMsg carries one batch. done is set once the channel closes, which
// is how the modal knows to stop saying it is still looking.
type imagesFoundMsg struct {
	batch []foundImage
	done  bool
}

// waitForImages reads ONE batch and returns it as a message. The receiver
// re-issues it until done, which is the standard way to pump a channel into
// Bubbletea: a command that blocks forever would never let the UI redraw.
func waitForImages(ch <-chan []foundImage) tea.Cmd {
	return func() tea.Msg {
		batch, ok := <-ch
		return imagesFoundMsg{batch: batch, done: !ok}
	}
}

// homeDir is the scan root. A failure here is not worth an error path -- the
// current directory is a reasonable place to look for a disk image, and the
// tree browser is still there for anywhere else.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
