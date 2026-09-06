package qemu

import "errors"

// ErrNoXattr means a 9p export's backing directory sits on a filesystem that
// cannot store the user.* extended attributes security_model=mapped-xattr
// needs.
var ErrNoXattr = errors.New("filesystem does not support extended attributes")
