package hostcheck

import (
	"os/exec"
	"path/filepath"
)

// Check is one host requirement and what to do when it is missing.
//
// Fix is a command list, not a preformatted string. A future caller can run
// each command directly instead of parsing one. Today nothing runs it: the
// installer only prints it.
type Check struct {
	Name   string
	OK     bool
	Detail string   // "/usr/bin", "not found", "permission denied"
	Fix    []string // shell commands, already distro-resolved; empty when OK
	// Optional marks a binary some commands need; a missing one is not a broken host.
	Optional bool
}

// binChecks are the executables stoat shells out to. qemu-system-x86_64 and
// qemu-img come from one package on Arch and two on Debian, which is exactly
// why Pkg names them per distro rather than per binary.
var binChecks = []struct {
	name     string
	pkg      Pkg
	optional bool
}{
	{"qemu-system-x86_64", Pkg{Arch: "qemu-full", Debian: "qemu-system-x86", Fedora: "qemu-kvm"}, false},
	{"qemu-img", Pkg{Arch: "qemu-full", Debian: "qemu-utils", Fedora: "qemu-img"}, false},
	{"ssh", Pkg{Arch: "openssh", Debian: "openssh-client", Fedora: "openssh-clients"}, false},
	{"xorriso", Pkg{Arch: "libisoburn", Debian: "xorriso", Fedora: "xorriso"}, false},
	{"git", Pkg{Arch: "git", Debian: "git", Fedora: "git"}, true},
}

func lookPathCheck(name string, fix []string) Check {
	path, err := exec.LookPath(name)
	if err != nil {
		return Check{Name: name, Detail: "not found", Fix: fix}
	}
	return Check{Name: name, OK: true, Detail: filepath.Dir(path)}
}

// Problems is the subset that failed, in the same order.
func Problems(cs []Check) []Check {
	var out []Check
	for _, c := range cs {
		if !c.OK && !c.Optional {
			out = append(out, c)
		}
	}
	return out
}
