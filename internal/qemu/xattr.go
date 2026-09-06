//go:build !windows

package qemu

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// xattrOK reports whether dir's filesystem supports the user.* extended
// attributes mapped-xattr stores its metadata in.
//
// A failure refuses the start rather than falling back to
// security_model=none: under `none` a guest-created symlink is a real
// symlink on the host, so a host process walking the share can be led
// anywhere. mapped-xattr turns it into an ordinary file containing the
// target string instead.
//
// This can only fire when the data root is on NFS, CIFS, exFAT, or a
// filesystem mounted nouser_xattr.
//
// The probe writes to dir itself, not a temp file inside it, since xattr
// support is a property of the mount.
func xattrOK(dir string) error {
	const probe = "user.stoat.probe"
	if err := unix.Setxattr(dir, probe, []byte{1}, 0); err != nil {
		// ENOTSUP and EOPNOTSUPP are the same errno on Linux. EPERM/EACCES
		// show up on hardened or nouser_xattr mounts and mean the same
		// thing here: the metadata cannot be stored on dir.
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			return fmt.Errorf("%w: %s (9p sharing needs user.* xattrs; is it on NFS, exFAT, or mounted nouser_xattr?)", ErrNoXattr, dir)
		}
		return fmt.Errorf("checking xattr support on %s: %w", dir, err)
	}
	_ = unix.Removexattr(dir, probe)
	return nil
}
