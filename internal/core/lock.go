package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrProvisionInProgress is returned when a second Apply finds
// <VM dir>/provision.lock already held. Two concurrent runs against the
// same guest each start apk (or the guest's own package manager); the
// second one hits apk's database lock and fails with exit 99. Refusing the
// second Apply up front avoids that race instead of surfacing it as a
// guest-side failure.
var ErrProvisionInProgress = errors.New("provision already in progress")

// WithProvisionLock runs fn while holding an exclusive, non-blocking flock
// on dir/provision.lock. It returns ErrProvisionInProgress without calling
// fn when another process already holds the lock.
//
// flock ties the lock to the holding process. A crashed run releases it
// when the kernel closes its file descriptors, so a lock file left on disk
// after a crash never blocks the next run. The file itself is never removed.
//
// Apply is the only caller: it takes this lock itself around sshx.Provision.
func WithProvisionLock(dir string, fn func() error) error {
	path := filepath.Join(dir, "provision.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open provision lock: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrProvisionInProgress
		}
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}
