package qemu

import (
	"fmt"
	"os"

	"github.com/novusedge/stoat/internal/config"
)

// prepareShares creates and validates the 9p export directories.
//
// It exists as a separate step because Args is pure and must not touch the
// filesystem, while QEMU treats a missing export path as a fatal start error
// (a raw stderr line) rather than a warning.
func prepareShares(v *config.VM) error {
	// The writable export is stoat's own, so stoat creates it. 0700: the
	// guest can write here and it's not meant for other host users.
	work := v.WorkDir()
	if err := os.MkdirAll(work, 0o700); err != nil {
		return fmt.Errorf("creating work share for %s: %w", v.Name, err)
	}
	if err := xattrOK(work); err != nil {
		return err
	}

	if v.Share == "" {
		return nil
	}
	// The read-only export is the user's directory. Never create it: a typo
	// in vm.toml should be reported, not turned into an empty folder that
	// looks like their files vanished.
	st, err := os.Stat(v.Share)
	if err != nil {
		return fmt.Errorf("share %s for %s: %w", v.Share, v.Name, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("share %s for %s is not a directory", v.Share, v.Name)
	}
	return nil
}
