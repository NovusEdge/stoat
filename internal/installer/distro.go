package installer

import (
	"os"
	"strings"
)

// Distro is as much as we need to know about the host's package manager to
// print a working install command. Anything we do not recognise stays
// DistroUnknown, and the installer names the packages without inventing a
// command for them — a wrong sudo line is worse than no line.
//
// Adding a distro touches three places: known() (the ID/ID_LIKE names it
// answers to), PkgName (which Pkg field is its package name), and
// installPrefix (its install command, the one place that mapping lives).
type Distro int

const (
	DistroUnknown Distro = iota
	DistroArch
	DistroDebian
	DistroFedora
)

// Pkg is one dependency's name across the distros we know. The same binary is
// packaged under a different name almost everywhere.
type Pkg struct{ Arch, Debian, Fedora string }

// ParseOSRelease pulls ID and ID_LIKE out of /etc/os-release content. The
// format is shell-ish: KEY=value, values optionally quoted, ID_LIKE holding a
// space-separated list. Everything else in the file is ignored.
func ParseOSRelease(content string) (id string, idLike []string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "ID":
			id = value
		case "ID_LIKE":
			if value != "" {
				idLike = strings.Fields(value)
			}
		}
	}
	return id, idLike
}

// DistroFrom resolves ID first, then falls back to ID_LIKE. A derivative that
// names itself (cachyos, ubuntu) is unknown by ID but recognisable by what it
// says it is like.
func DistroFrom(id string, idLike []string) Distro {
	if d := known(id); d != DistroUnknown {
		return d
	}
	for _, like := range idLike {
		if d := known(like); d != DistroUnknown {
			return d
		}
	}
	return DistroUnknown
}

func known(name string) Distro {
	switch name {
	case "arch":
		return DistroArch
	case "debian", "ubuntu":
		return DistroDebian
	case "fedora", "rhel":
		return DistroFedora
	}
	return DistroUnknown
}

// DetectDistro reads the host's /etc/os-release. An unreadable or missing file
// is not an error worth surfacing — it just means we print package names
// without a command.
func DetectDistro() Distro {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return DistroUnknown
	}
	return DistroFrom(ParseOSRelease(string(b)))
}

// PkgName is this distro's name for p, or "" when we do not know the distro.
func (d Distro) PkgName(p Pkg) string {
	switch d {
	case DistroArch:
		return p.Arch
	case DistroDebian:
		return p.Debian
	case DistroFedora:
		return p.Fedora
	}
	return ""
}

// installPrefix is each known distro's command up to the package name(s).
var installPrefix = map[Distro]string{
	DistroArch:   "sudo pacman -S --needed ",
	DistroDebian: "sudo apt install ",
	DistroFedora: "sudo dnf install ",
}

// InstallCmd is the shell command that installs p, or -- when the distro is
// unknown -- the package's names with no command attached. It returns a slice
// because a Check's Fix is a command *list*, see checks.go for why that
// matters: a wrong sudo line is worse than no line, but no line at all loses
// information the user could have used.
func (d Distro) InstallCmd(p Pkg) []string {
	name := d.PkgName(p)
	if name == "" {
		return namesOnly(p)
	}
	prefix, ok := installPrefix[d]
	if !ok {
		return namesOnly(p)
	}
	return []string{prefix + name}
}

// namesOnly is what an unrecognised distro gets: every candidate package name
// we know, joined for a human to pick from, with no package manager invented.
func namesOnly(p Pkg) []string {
	return []string{"install: " + p.Arch + " / " + p.Debian + " / " + p.Fedora}
}
