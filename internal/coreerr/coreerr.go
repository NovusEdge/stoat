// Package coreerr holds the sentinel errors internal/core and
// internal/capabilities both need to identify by identity (errors.Is).
// capabilities cannot import core directly: core imports provider, provider
// imports capabilities, and a capabilities->core edge would close that
// cycle. core re-exports these under its own names so every existing
// core.ErrXxx caller compiles unchanged.
package coreerr

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrInvalidSpec = errors.New("invalid spec")
	ErrBroken      = errors.New("broken vm.toml")
)

// NoVMError is the one "VM not found" error. Known is called only when the
// message is rendered, because a failed config.Load inside List's scan of
// non-VM directories would otherwise list the data root once per directory.
type NoVMError struct {
	Name  string
	Known func() []string
}

// Error renders the CLI wording. Text takes the tool that lists VMs, because
// an MCP client has no `stoat ls`.
func (e *NoVMError) Error() string { return e.Text("stoat ls") }

func (e *NoVMError) Text(list string) string {
	msg := fmt.Sprintf("no VM %q", e.Name)
	if e.Known != nil {
		if s := Suggest(e.Name, e.Known()); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		}
	}
	return msg + "; see " + list
}

// MCPText is Error with the MCP tool that lists VMs.
func (e *NoVMError) MCPText() string { return e.Text("list_vms") }

// Is matches fs.ErrNotExist too: config.Load used to return the raw stat
// error, and callers that test for absence keep working.
func (e *NoVMError) Is(target error) bool {
	return target == ErrNotFound || target == fs.ErrNotExist
}

// NoImageError is ErrNotFound for an image id that is neither in the catalog
// nor a local file.
type NoImageError struct {
	Spec  string
	Known []string
}

func (e *NoImageError) Error() string { return e.Text("run stoat images") }

func (e *NoImageError) Text(list string) string {
	msg := fmt.Sprintf("no image %q", e.Spec)
	if s := Suggest(e.Spec, e.Known); s != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", s)
	}
	return msg + "; " + list
}

// MCPText is Error with the MCP tool that lists images.
func (e *NoImageError) MCPText() string { return e.Text("call list_images") }

func (e *NoImageError) Is(target error) bool { return target == ErrNotFound }

// Suggest returns the candidate closest to name, or "" when none is close
// enough to be a plausible typo.
func Suggest(name string, candidates []string) string {
	best, bestDist := "", len(name)/3+2
	for _, c := range candidates {
		d := editDistance(strings.ToLower(name), strings.ToLower(c))
		if strings.Contains(strings.ToLower(c), strings.ToLower(name)) && name != "" {
			d = 1
		}
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
