package recipes

import (
	"fmt"

	"github.com/novusedge/stoat/internal/guest"
)

// interpreterCommand maps a runtime to the guest command that reads a script
// from stdin. "sh -s" and "python3 -" both read a script body piped to
// them, matching how sshx.Provision pipes ScriptBody as the recipe's stdin.
var interpreterCommand = map[string][]string{
	"sh":      {"sh", "-s"},
	"python3": {"python3", "-"},
}

// InterpreterArgs returns the guest command that runs a recipe body under
// runtime, for use as the ssh command's trailing argv.
func InterpreterArgs(runtime string) []string {
	if args, ok := interpreterCommand[runtime]; ok {
		return args
	}
	return []string{runtime}
}

// BootstrapScript returns a sh snippet that installs runtime on osName if
// missing, or "" when runtime needs no install step ("sh" is always present
// in a POSIX guest) or osName is not a loaded guest. sshx.Provision pipes
// this to `sh -s` over ssh before running a recipe under a non-sh runtime.
// It calls the guest prelude's verbs rather than a package manager by name,
// so the same snippet works on any guest that defines runtime_packages for
// the runtime.
func BootstrapScript(runtime, osName string) string {
	if runtime == "sh" {
		return ""
	}
	o, ok := guest.Lookup(osName)
	if !ok {
		return ""
	}
	pkg := o.Pkg.RuntimePackages[runtime]
	if pkg == "" {
		return ""
	}
	return fmt.Sprintf("set -e\nif ! command -v %s >/dev/null 2>&1; then\nstoat_pkg_setup\nstoat_pkg_install %s\nfi\n", runtime, pkg)
}
