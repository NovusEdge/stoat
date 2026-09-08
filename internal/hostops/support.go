// Package hostops owns the narrow qualification gate for native VM work.
package hostops

import (
	"errors"
	"fmt"
)

// ErrUnsupported reports a native host whose VM operations have not been
// qualified yet. Only RequireLocalHypervisor returns it, so a command that
// starts or stops a local VM refuses while one that reads or edits a VM
// record does not.
var ErrUnsupported = errors.New("native VM operations are not qualified")

// requirement names the accelerator a host needs before stoat qualifies it,
// and the issue tracking that work. Keyed by GOOS/GOARCH so Message stays a
// lookup instead of a chain of string comparisons.
type requirement struct {
	accelerator string
	issue       int
}

var requirements = map[string]requirement{
	"darwin/arm64":  {"the QEMU HVF accelerator", 82},
	"windows/amd64": {"the QEMU WHPX accelerator", 83},
}

// Message builds the diagnostic for ErrUnsupported on goos/goarch. It is the
// single source for this text: the CLI error path, `stoat doctor`, and the
// JSON payload all render it instead of keeping their own copies, so the
// three surfaces cannot drift out of wording with each other.
//
// goos and goarch are parameters, not runtime.GOOS/runtime.GOARCH, so the
// message can be exercised in a table test on a Linux build machine for
// hosts that build never runs on.
func Message(goos, goarch string) string {
	host := goos + "/" + goarch
	req, known := requirements[host]
	lines := []string{
		fmt.Sprintf("native VM operations are not qualified on %s.", host),
	}
	if known {
		lines = append(lines, fmt.Sprintf("%s needs a qualified runtime with %s (tracked in stoat#%d).", host, req.accelerator, req.issue))
	} else {
		lines = append(lines, fmt.Sprintf("%s has no qualified runtime yet.", host))
	}
	lines = append(lines,
		"starting and stopping a local VM needs a qualified host; commands that only read or edit VM records still work here.",
		"Linux with KVM is the supported configuration today.",
	)
	out := lines[0]
	for _, l := range lines[1:] {
		out += "\n" + l
	}
	return out
}
