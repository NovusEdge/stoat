package recipes

import "fmt"

// installCommand maps a guest OS to the shell command that installs a
// package by name, one word per argument the way apk/apt/pacman/dnf all
// accept. BootstrapScript appends the package name itself.
var installCommand = map[string][]string{
	"alpine": {"apk", "--wait", "60", "add"},
	"ubuntu": {"apt-get", "install", "-y"},
	"debian": {"apt-get", "install", "-y"},
	"arch":   {"pacman", "-S", "--noconfirm"},
	"fedora": {"dnf", "install", "-y"},
}

// runtimePackage maps a non-sh runtime to the package name that provides it
// per guest OS. Arch names its Python 2 successor package "python", not
// "python3"; every other OS here uses "python3".
var runtimePackage = map[string]map[string]string{
	"python3": {
		"alpine": "python3",
		"ubuntu": "python3",
		"debian": "python3",
		"fedora": "python3",
		"arch":   "python",
	},
}

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
// in a POSIX guest). sshx.Provision pipes this to `sh -s` over ssh before
// running a recipe under a non-sh runtime.
//
// The check-then-install shape, and apk's --wait 60, mirror the bundled
// recipes' own idempotent-install pattern (internal/recipes/bundled), so a
// recipe re-applied on a VM that already has the runtime does nothing.
func BootstrapScript(runtime, osName string) string {
	if runtime == "sh" {
		return ""
	}
	pkg := runtimePackage[runtime][osName]
	if pkg == "" {
		return ""
	}
	install := installCommand[osName]
	if install == nil {
		return ""
	}
	cmd := ""
	for _, w := range install {
		cmd += w + " "
	}
	return fmt.Sprintf("set -e\nif ! command -v %s >/dev/null 2>&1; then\n%s%s\nfi\n", runtime, cmd, pkg)
}
