package qemu

import "bytes"

// cmdlineMatches reports whether a /proc/<pid>/cmdline blob belongs to the VM
// whose directory is dir. It anchors on dir+"/" rather than a bare substring
// match: cmdline always contains "-pidfile <dir>/qemu.pid", so the trailing
// separator is present for a genuine match, but a bare Contains would also
// match a sibling VM whose directory name has dir's as a prefix (e.g. "work"
// matching inside "work2").
func cmdlineMatches(cmdline []byte, dir string) bool {
	return bytes.Contains(cmdline, []byte(dir+"/"))
}
