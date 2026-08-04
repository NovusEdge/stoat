package recipes

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/novusedge/stoat/internal/guest"
)

// Metadata is a phase-2 shell recipe's declared front matter
// (docs/design/guest-subsystem.md §5): who it is, which OSes it applies to,
// what guest capabilities it needs, and what stages it runs in. A recipe
// with no front-matter block at all parses to the zero Metadata: that is
// not an error, it just declares nothing (see List, which already treats an
// unmetadata'd recipe as always offered).
type Metadata struct {
	Name        string
	Description string
	OS          []string // empty means "no OS restriction declared"
	Requires    []string // capability names, resolved against guest.OS
	Stages      []string
}

// stoatTag matches one front-matter line: "#", optional whitespace,
// "stoat:", then the rest of the line, key and value still combined.
var stoatTag = regexp.MustCompile(`^#\s*stoat:(.*)$`)

// metadataKeys are the front-matter keys ParseMetadata understands. An
// unrecognised key is reported, not ignored: a recipe that declares
// something wrong needs to be visible, not silently treated as declaring
// nothing (docs/design/guest-subsystem.md §5).
var metadataKeys = map[string]bool{
	"name": true, "description": true, "os": true, "requires": true, "stages": true,
}

// ParseMetadata reads the front-matter block from a recipe body: "# stoat:
// key value" comment lines starting right after an optional shebang and
// ending at the first line that is neither blank nor a comment. A
// "# stoat:" tag past that point is not front matter: "front-matter means
// front" is positional, not "anywhere in the file", so it is left as an
// ordinary comment rather than honoured or reported.
//
// Every other problem in the block is reported, not swallowed: an unknown
// key, a key declared twice, a tag with no key at all, and a key with no
// value are all parse errors, collected and returned together rather than
// stopping at the first one.
func ParseMetadata(body string) (Metadata, error) {
	var m Metadata
	var errs []string
	seen := map[string]bool{}

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if i == 0 && strings.HasPrefix(trimmed, "#!") {
			continue // shebang: not front matter, doesn't end it either
		}
		if trimmed == "" {
			continue // blank lines don't end the block
		}
		match := stoatTag.FindStringSubmatch(trimmed)
		if match == nil {
			if !strings.HasPrefix(trimmed, "#") {
				break // first non-comment line ends front matter
			}
			continue // an ordinary comment, still inside the block
		}

		rest := strings.TrimSpace(match[1])
		if rest == "" {
			errs = append(errs, fmt.Sprintf("malformed line %q: no key after \"stoat:\"", trimmed))
			continue
		}
		key, value := rest, ""
		if idx := strings.IndexAny(rest, " \t"); idx >= 0 {
			key, value = rest[:idx], strings.TrimSpace(rest[idx:])
		}

		if !metadataKeys[key] {
			errs = append(errs, fmt.Sprintf("unknown key %q", key))
			continue
		}
		if seen[key] {
			errs = append(errs, fmt.Sprintf("duplicate key %q", key))
			continue
		}
		seen[key] = true
		if value == "" {
			errs = append(errs, fmt.Sprintf("key %q has no value", key))
			continue
		}

		switch key {
		case "name":
			m.Name = value
		case "description":
			m.Description = value
		case "os":
			m.OS = splitMetadataList(value)
		case "requires":
			m.Requires = splitMetadataList(value)
		case "stages":
			m.Stages = splitMetadataList(value)
		}
	}

	if len(errs) > 0 {
		return Metadata{}, fmt.Errorf("recipe front matter: %s", strings.Join(errs, "; "))
	}
	return m, nil
}

// splitMetadataList parses a comma-separated front-matter value ("alpine,
// ubuntu, debian"). Empty items from stray commas are dropped rather than
// reported: a formatting slip here isn't the class of error §5 asks to be
// caught, unlike an unknown key or a missing value.
func splitMetadataList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ReadMetadata reads and parses name's front matter. name is the full
// filename, exactly as Read and List expect it.
func ReadMetadata(name string) (Metadata, error) {
	body, err := Read(name)
	if err != nil {
		return Metadata{}, err
	}
	return ParseMetadata(body)
}

// capabilities are the guest capabilities a recipe's "requires" tag can
// name, and how each resolves against a guest.OS. Kept to exactly what a
// bundled recipe declares (see the annotated .sh files); add an entry here
// when a recipe actually needs a new one.
//
// "systemd" is derived from OS.Backend rather than a dedicated field: today
// the apkovl backend is Alpine, and Alpine alone in the registry is OpenRC,
// so "not apkovl" and "has systemd" happen to coincide exactly. That is a
// real limitation, not a general init-system model; see the package
// report for what a future OS with a different combination would need.
var capabilities = map[string]func(guest.OS) bool{
	"systemd": func(g guest.OS) bool { return g.Backend != "apkovl" },
}

// initSystem names osName's init system for a "requires" mismatch message.
func initSystem(g guest.OS) string {
	if g.Backend == "apkovl" {
		return "openrc"
	}
	return "systemd"
}

// UnsupportedReason explains why osName cannot run a recipe declaring m, or
// returns "" if it can. os is checked first (a named mismatch), then each
// capability in Requires in order, stopping at the first failure: one
// reason per recipe, matching core.RecipeIssue's shape.
func UnsupportedReason(osName string, m Metadata) string {
	if len(m.OS) > 0 {
		ok := false
		for _, o := range m.OS {
			if o == osName {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("built for %s, not %s", strings.Join(m.OS, ", "), osName)
		}
	}

	g, known := guest.Lookup(osName)
	if !known {
		if len(m.Requires) > 0 {
			return fmt.Sprintf("%q is not a recognised OS", osName)
		}
		return ""
	}
	for _, cap := range m.Requires {
		check, ok := capabilities[cap]
		if !ok {
			return fmt.Sprintf("requires unknown capability %q", cap)
		}
		if !check(g) {
			switch cap {
			case "systemd":
				return fmt.Sprintf("requires systemd, %s uses %s", g.Name, initSystem(g))
			default:
				return fmt.Sprintf("requires %s, %s does not have it", cap, g.Name)
			}
		}
	}
	return ""
}
