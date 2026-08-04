// Package guest owns everything stoat knows about guest operating systems:
// the login shell a seeded account needs, the packages a base seed assumes
// but an image may not ship, which provisioning backend applies, the
// interactive installer's name, the default ssh user, the package-manager
// commands a scaffolded recipe uses, and the filename hints that recognise
// the OS in a bring-your-own image.
//
// Adding an OS means adding one entry to registry below, and every field
// must be filled: a missing field does not fail loudly, it fails silently,
// as an unselectable catalog entry, an empty OS that hands cloud-init a shell
// the image doesn't have, or a VM offered zero recipes. See
// docs/design/guest-subsystem.md for the incident that made this the rule.
package guest

// InitSystem identifies which init system a guest OS boots. It exists
// because "requires systemd" in a recipe's front matter is a real fact
// about the guest, not a proxy for something else (see the Init field doc
// on OS).
type InitSystem string

const (
	InitSystemd InitSystem = "systemd"
	InitOpenRC  InitSystem = "openrc"
)

// OS is one guest operating system's facts, gathered from the sites that
// used to decide them ad hoc (see docs/design/guest-subsystem.md §4 for the
// full inventory of call sites this replaces).
type OS struct {
	Name string // canonical identity, matches vm.toml's os field

	// Shell is the login shell for a seeded account. It MUST exist in the
	// image: cloud-init's user module fails outright on a missing shell,
	// leaving no account and no authorized_keys, and the only symptom is
	// "Permission denied (publickey)" forever.
	Shell string

	// SeedPackages are packages the base seed ASSUMES are present but which
	// this image does not ship. Alpine needs sudo: the users: sudo key
	// writes a sudoers fragment, and the binary is absent (the cloud-init
	// aport prefers doas).
	SeedPackages []string

	// Backend names how this OS is provisioned: "apkovl" | "cloudinit" |
	// "ssh". This stays a string here, not the Backend interface itself:
	// package guest owns OS data only and must stay a zero-import leaf, so
	// the interface lives in internal/backend, one level up. See
	// docs/design/guest-subsystem.md §3.2.
	Backend string

	// Init is the guest's init system. It is its own field rather than
	// derived from Backend: apkovl and OpenRC both happen to mean Alpine
	// today, but the two facts are independent (a BYO cloudinit image could
	// run OpenRC, or a future apkovl-based distro could run systemd), and
	// a recipe's "requires systemd" needs to ask the real question.
	Init InitSystem

	// Installer is the interactive install command named in UI hints.
	// Empty means "the installer" generically.
	Installer string

	// DefaultSSHUser is who to connect as when nothing overrides it.
	DefaultSSHUser string

	// PkgSetup is the package-manager preamble a scaffolded recipe needs
	// before it can install anything: enabling a repository, refreshing
	// indexes, or nothing if the OS needs neither.
	PkgSetup string

	// PkgInstall is the install verb a scaffolded recipe appends package
	// names to, e.g. "apk add ".
	PkgInstall string

	// FilenameHints recognise this OS in a BYO image filename. An empty OS
	// on a BYO image is the dangerous case: it silently selects every
	// default, which is how an Alpine image gets asked for /bin/bash.
	FilenameHints []string

	// CloudRecipes reports whether an OS is offered the shared
	// "*.cloud.yaml" fragment set at all: true does not mean every shared
	// fragment applies. cloud-init's packages: list has no per-distro
	// syntax, so devtools.cloud.yaml's names happen to hold for Alpine's
	// apk too, but xfce.cloud.yaml's systemd runcmd does not (Alpine is
	// OpenRC): Alpine gets devtools.cloud.yaml from the shared set and its
	// own xfce.alpine.cloud.yaml, exactly like Fedora gets its own
	// xfce.fedora.cloud.yaml. See recipes/recipes.go's List doc comment for
	// how a per-OS fragment and the shared set combine.
	CloudRecipes bool
}

// registry is the single home for every guest-OS fact. See the package doc
// comment: adding an OS means adding one complete entry here.
var registry = []OS{
	{
		Name:           "alpine",
		Shell:          "/bin/ash",
		SeedPackages:   []string{"sudo"},
		Backend:        "apkovl",
		Init:           InitOpenRC, // Alpine has shipped OpenRC as its init since its first release; it has never carried systemd
		Installer:      "setup-alpine",
		DefaultSSHUser: "root",
		PkgSetup: "# -c enables the community repository (docker, tailscale and most of what\n" +
			"# you would want live there, not in main); -1 picks a mirror and refreshes\n" +
			"# the indexes, so a separate `apk update` is redundant.\nsetup-apkrepos -c -1\n",
		PkgInstall:    "apk add ",
		FilenameHints: []string{"alpine"},
		CloudRecipes:  true,
	},
	{
		Name:           "ubuntu",
		Shell:          "/bin/bash",
		SeedPackages:   nil,
		Backend:        "cloudinit",
		Init:           InitSystemd, // Ubuntu switched from Upstart to systemd in 15.04 (2015); every supported release since is systemd
		Installer:      "",
		DefaultSSHUser: "stoat",
		PkgSetup:       "export DEBIAN_FRONTEND=noninteractive\napt-get update\n",
		PkgInstall:     "apt-get install -y ",
		FilenameHints:  []string{"ubuntu"},
		CloudRecipes:   true,
	},
	{
		Name:           "debian",
		Shell:          "/bin/bash",
		SeedPackages:   nil,
		Backend:        "cloudinit",
		Init:           InitSystemd, // Debian made systemd the default init in Debian 8 "jessie" (2015)
		Installer:      "",
		DefaultSSHUser: "stoat",
		PkgSetup:       "export DEBIAN_FRONTEND=noninteractive\napt-get update\n",
		PkgInstall:     "apt-get install -y ",
		FilenameHints:  []string{"debian"},
		CloudRecipes:   true,
	},
	{
		Name:           "fedora",
		Shell:          "/bin/bash",
		SeedPackages:   nil,
		Backend:        "cloudinit",
		Init:           InitSystemd, // Fedora was systemd's original adopter, default since Fedora 15 (2011)
		Installer:      "",
		DefaultSSHUser: "stoat",
		PkgSetup:       "",
		PkgInstall:     "dnf install -y ",
		FilenameHints:  []string{"fedora"},
		CloudRecipes:   false,
	},
	{
		Name:           "arch",
		Shell:          "/bin/bash",
		SeedPackages:   nil,
		Backend:        "cloudinit",
		Init:           InitSystemd, // Arch replaced its own initscripts with systemd as the default in 2012
		Installer:      "",
		DefaultSSHUser: "stoat",
		PkgSetup:       "pacman -Sy --noconfirm\n",
		PkgInstall:     "pacman -S --noconfirm ",
		FilenameHints:  []string{"arch"},
		CloudRecipes:   true,
	},
}

// Lookup returns the OS declared for name, or false if name is not in the
// registry. It never guesses: an unknown OS is reported as unknown, and it
// is the caller's job to decide what that means.
func Lookup(name string) (OS, bool) {
	for _, o := range registry {
		if o.Name == name {
			return o, true
		}
	}
	return OS{}, false
}

// All returns every declared OS.
func All() []OS {
	out := make([]OS, len(registry))
	copy(out, registry)
	return out
}

// Names returns the canonical name of every declared OS, in registry order.
func Names() []string {
	out := make([]string, len(registry))
	for i, o := range registry {
		out[i] = o.Name
	}
	return out
}
