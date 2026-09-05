package core

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/qemu"
)

// Shot is one screenshot: where it landed and what is in it.
type Shot struct {
	Path   string
	Bytes  int64
	Width  int
	Height int
}

// Screenshot writes VM name's screen to dest, or to
// <vm dir>/screenshots/<RFC3339 seconds, colons stripped>.png when dest is
// empty. See shotPath.
//
// The running check is here rather than in qemu.Screenshot so a stopped VM
// answers ErrNotRunning, the same error Stop gives, instead of the
// monitor-socket error a caller cannot act on differently.
func Screenshot(name, dest string) (Shot, error) {
	v, err := load(name)
	if err != nil {
		return Shot{}, err
	}
	if !qemu.Running(v) {
		return Shot{}, fmt.Errorf("%w: %s", ErrNotRunning, name)
	}

	if dest == "" {
		dir := filepath.Join(v.Dir, "screenshots")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Shot{}, err
		}
		dest = shotPath(dir, time.Now().UTC())
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return Shot{}, err
	}
	if err := qemu.Screenshot(v, dest); err != nil {
		return Shot{}, err
	}

	fi, err := os.Stat(dest)
	if err != nil {
		return Shot{}, err
	}
	f, err := os.Open(dest)
	if err != nil {
		return Shot{}, err
	}
	defer func() { _ = f.Close() }()
	// The header alone gives the size, so a 4K frame is never decoded into
	// memory to answer two integers.
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return Shot{}, err
	}
	return Shot{Path: dest, Bytes: fi.Size(), Width: cfg.Width, Height: cfg.Height}, nil
}

// shotPath names a screenshot after the second it was taken: RFC3339 with
// the colons stripped, since a colon in a filename breaks scp's host:path
// split and Windows refuses it outright.
//
// A second shot inside the same second gets -2, then -3. A caller polling a
// boot takes frames faster than the name's resolution, and overwriting the
// frame that showed the stall is the failure this avoids.
func shotPath(dir string, at time.Time) string {
	stamp := strings.ReplaceAll(at.Format(time.RFC3339), ":", "")
	path := filepath.Join(dir, stamp+".png")
	for n := 2; ; n++ {
		// Any stat failure other than "gone" is answered the same way: the
		// name is used, and qemu reports the write failure with the reason.
		if _, err := os.Stat(path); err != nil {
			return path
		}
		path = filepath.Join(dir, stamp+"-"+strconv.Itoa(n)+".png")
	}
}
