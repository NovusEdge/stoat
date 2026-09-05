package project

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Share is one project directory exposed to a VM over 9p.
//
// Tag is qemu's mount_tag. qemu derives the fsdev id from it, so two shares
// on one VM must not share a tag. "host" and "work" are reserved: qemu.Args
// already attaches the legacy single share and the per-VM scratch export
// under those two names.
type Share struct {
	Tag   string
	Host  string
	Guest string
}

// Shares resolves one declaration's shares entries to absolute host paths and
// guest mountpoints.
//
// Every entry is resolved through filepath.EvalSymlinks before the
// containment check. A plain string comparison passes "link" that points at
// /etc, which would hand the guest a writable mount of the host's config.
func (p *Project) Shares(key string) ([]Share, error) {
	v, ok := p.byKey[key]
	if !ok {
		return nil, fmt.Errorf("%s: no vms.%s", FileName, key)
	}
	root, err := filepath.EvalSymlinks(p.Dir)
	if err != nil {
		return nil, err
	}

	var out []Share
	guests := map[string]string{}
	for i, entry := range v.Shares {
		// filepath.Join already cleans ".." components, so an escape shows up
		// as a lexical prefix mismatch before the entry needs to exist at
		// all. That check runs first: a share need not exist for this error,
		// only for the EvalSymlinks call below, which resolves the escape a
		// symlink hides.
		joined := filepath.Join(p.Dir, entry)
		if joined != p.Dir && !strings.HasPrefix(joined, p.Dir+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s: vms.%s.shares: %q is outside the project", FileName, key, entry)
		}
		abs, err := filepath.EvalSymlinks(joined)
		if err != nil {
			return nil, fmt.Errorf("%s: vms.%s.shares: %q: %v", FileName, key, entry, err)
		}
		if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s: vms.%s.shares: %q is outside the project", FileName, key, entry)
		}

		guest := "/work"
		if abs != root {
			guest = "/work/" + filepath.Base(abs)
		}
		if first, dup := guests[guest]; dup {
			return nil, fmt.Errorf("%s: vms.%s.shares: %q and %q both mount at %s",
				FileName, key, first, entry, guest)
		}
		guests[guest] = entry

		out = append(out, Share{Tag: fmt.Sprintf("p%d", i), Host: abs, Guest: guest})
	}
	return out, nil
}
