// Package vmname owns the rule for a VM name. A name becomes a directory
// under the data root, and filepath.Base of that directory reads it back, so
// every entry point that accepts a new name validates it here: internal/core's
// create path, internal/project's stoat.toml loader, and internal/mcpsrv's
// tool guards.
//
// The rule applies on every platform. A data root is portable, and a VM
// created on Linux must stay openable on Windows.
package vmname

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/novusedge/stoat/internal/coreerr"
)

// nameRE is the grammar. It excludes an empty name, a leading dash and a
// space by construction; the checks before it name the specific problem
// instead of pointing at the pattern.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// hint states what passes, appended to every rejection.
const hint = "a name is letters, digits, dot, dash or underscore, and starts with a letter or digit"

// reserved are the Windows device names. Windows resolves one at every path
// level, with or without an extension, so a VM directory called "nul" opens a
// device instead of a directory.
var reserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// Validate reports why name cannot be a VM name, or nil. Every error wraps
// coreerr.ErrInvalidSpec, which internal/cli/wire reports as invalid_spec.
//
// Validate never rewrites a name. A rewrite hides the attempt from the user
// who typed it.
func Validate(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errf("a vm name is required")
	case name != strings.TrimSpace(name):
		return errf("vm name %q has leading or trailing whitespace", name)
	case name == "." || name == "..":
		return errf("vm name %q is a path traversal", name)
	case strings.ContainsAny(name, `/\`):
		return errf("vm name %q contains a path separator", name)
	case strings.ContainsRune(name, 0):
		return errf("vm name %q contains a null byte", name)
	case strings.HasPrefix(name, "-"):
		return errf("vm name %q starts with a dash, which reads as a flag", name)
	case isReserved(name):
		return errf("vm name %q is a reserved device name on Windows", name)
	case !nameRE.MatchString(name):
		return errf("vm name %q must match %s", name, nameRE)
	}
	return nil
}

// isReserved matches a device name case-insensitively, with or without an
// extension. Windows also drops a trailing dot or space before it resolves a
// path, so "nul." and "nul " reach the same device.
func isReserved(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.TrimRight(stem, ". ")
	return reserved[strings.ToUpper(stem)]
}

func errf(format string, args ...any) error {
	return fmt.Errorf("%w: %s; %s", coreerr.ErrInvalidSpec, fmt.Sprintf(format, args...), hint)
}
