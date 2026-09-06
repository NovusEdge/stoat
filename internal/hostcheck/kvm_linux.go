//go:build linux

package hostcheck

import (
	"errors"
	"io/fs"
	"os"
)

// KVMCheck reports whether this user can open /dev/kvm read/write. That is
// the failure mode that matters, not whether the file exists.
//
// This file is the Linux-specific host-check seam. Other hosts report the
// native qualification boundary without claiming /dev/kvm as their runtime.
func KVMCheck() Check { return kvmCheckAt("/dev/kvm") }

func kvmCheckAt(path string) Check {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err == nil {
		_ = f.Close()
		return Check{Name: "/dev/kvm", OK: true, Detail: "read/write"}
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Check{
			Name:   "/dev/kvm",
			Detail: "not present: no KVM support, or the kvm module is not loaded",
			Fix:    []string{"sudo modprobe kvm_intel   # or kvm_amd"},
		}
	case errors.Is(err, fs.ErrPermission):
		return Check{
			Name:   "/dev/kvm",
			Detail: "permission denied",
			Fix: []string{
				`sudo usermod -aG kvm "$USER"`,
				"# then log out and back in, since group membership only applies to new sessions",
			},
		}
	}
	return Check{Name: "/dev/kvm", Detail: err.Error()}
}
