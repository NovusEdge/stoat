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
	Name     string
	OK       bool
	Detail   string   // "/usr/bin", "not found", "permission denied"
	Fix      []string // shell commands, already distro-resolved; empty when OK
	Optional bool
}

// binChecks are the executables stoat shells out to. qemu-system-x86_64 and
// qemu-img come from one package on Arch and two on Debian, which is exactly
// why Pkg names them per distro rather than per binary.
var binChecks = []struct {
	name string
	pkg  Pkg
}{
	{"qemu-system-x86_64", Pkg{Arch: "qemu-full", Debian: "qemu-system-x86", Fedora: "qemu-kvm"}},
	{"qemu-img", Pkg{Arch: "qemu-full", Debian: "qemu-utils", Fedora: "qemu-img"}},
	{"ssh", Pkg{Arch: "openssh", Debian: "openssh-client", Fedora: "openssh-clients"}},
	{"xorriso", Pkg{Arch: "libisoburn", Debian: "xorriso", Fedora: "xorriso"}},
}

// RunChecks probes every host requirement, in the order they are displayed.
// None of these block the install: internal/qemu/run.go already gates VM start
// at runtime with the same two probes, so the installer's job is to say it
// early, not to enforce it.
func RunChecks(d Distro) []Check {
	checks := make([]Check, 0, len(binChecks)+1)
	for _, b := range binChecks {
		checks = append(checks, lookPathCheck(b.name, d.InstallCmd(b.pkg)))
	}
	return append(checks, KVMCheck())
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
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}
