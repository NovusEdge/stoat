package apkovl

import "fmt"

// Mount9p describes one 9p share qemu attaches to a VM: its mount tag (qemu's
// mount_tag / virtfs id), the guest mountpoint, and the fstab options.
// internal/qemu/args.go's -virtfs flags and this package's fstab must agree
// on the tag, or the guest mounts nothing. internal/sshx's disk-VM mount step
// reads these same values, so a disk-installed guest gets the identical
// fstab line the live overlay would have written.
type Mount9p struct {
	Tag     string
	Dir     string
	Options string
}

// HostMount9p is the user's share directory, read-only. It applies only when
// the VM has Share configured; see qemu.Args.
var HostMount9p = Mount9p{
	Tag:     "host",
	Dir:     "/mnt/host",
	Options: "trans=virtio,version=9p2000.L,ro,_netdev,nofail",
}

// WorkMount9p is stoat's per-VM scratch export, writable. qemu attaches it
// unconditionally, so every VM mode gets a fstab line for it.
var WorkMount9p = Mount9p{
	Tag:     "work",
	Dir:     "/mnt/work",
	Options: "trans=virtio,version=9p2000.L,rw,_netdev,nofail",
}

// FstabLine renders m as one /etc/fstab entry, newline included.
func (m Mount9p) FstabLine() string {
	return fmt.Sprintf("%s %s 9p %s 0 0\n", m.Tag, m.Dir, m.Options)
}
