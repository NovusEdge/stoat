// Package guest owns every fact stoat knows about a guest operating system:
// the login shell for a seeded account, packages the base seed assumes but
// an image may not ship, the provisioning backend, the interactive
// installer's name, the default ssh user, the package-manager commands a
// scaffolded recipe uses, and the filename hints that recognise the OS in a
// bring-your-own image.
//
// Adding an OS means adding one entry to registry below. Every field must
// be filled. A missing field fails silently: an unselectable catalog entry,
// an OS that hands cloud-init a shell the image lacks, or a VM offered zero
// recipes. See docs/design/guest-subsystem.md for the incident that made
// this the rule.
package guest

// InitSystem identifies which init system a guest OS boots. It exists
// because "requires systemd" in a recipe's front matter is a real fact
// about the guest, not a proxy for something else (see the Init field doc
// on OS).
type InitSystem string

const (
	InitSystemd InitSystem = "systemd"
	InitOpenRC  InitSystem = "openrc"
	InitRC      InitSystem = "rc"
)

// OS is one guest operating system's facts, loaded from a guest.toml. See
// docs/reference/guest.md for the file.
type OS struct {
	Schema int    `toml:"schema"`
	Name   string `toml:"name"`

	// Shell is the login shell for a seeded account. It must exist in the
	// image. cloud-init's user module fails outright on a missing shell,
	// leaving no account and no authorized_keys. The only symptom is
	// "Permission denied (publickey)".
	Shell string `toml:"shell"`

	// Init is the guest's init system, a fact independent of the backend: a
	// BYO cloudinit image can run OpenRC. A recipe's "requires systemd"
	// needs the real answer.
	Init InitSystem `toml:"init"`

	// Installer is the interactive install command named in UI hints.
	// Empty means "the installer".
	Installer string `toml:"installer"`

	// DefaultBackend and DefaultSSHUser seed the VM fields at create time.
	// A catalog entry overrides both (iso.Entry), and every code path reads
	// the VM field, never these.
	DefaultBackend string `toml:"default_backend"`
	DefaultSSHUser string `toml:"default_ssh_user"`

	// Escalate is the argv that runs a command as root for a non-root ssh
	// user. sshx applies it only when the VM's ssh user is not root.
	Escalate []string `toml:"escalate"`

	// Capabilities feed recipe.toml's `requires`. The loader appends Init.
	Capabilities []string `toml:"capabilities"`

	// Aliases are extra keys a recipe's [scripts] map may use for this OS,
	// tried after Name.
	Aliases []string `toml:"aliases"`

	// FilenameHints recognise this OS in a BYO image filename.
	FilenameHints []string `toml:"filename_hints"`

	// SeedPackages are packages the cloud-init seed assumes but the image
	// does not ship. Alpine needs sudo: its cloud-init aport ships doas.
	SeedPackages []string `toml:"seed_packages"`

	Pkg Pkg               `toml:"pkg"`
	Svc Svc               `toml:"svc"`
	Cmd map[string]string `toml:"cmd"`

	// Backends holds one opaque table per backend name. The backend package
	// that owns the name decodes it; this package never reads inside.
	Backends map[string]map[string]any `toml:"backend"`

	// Source is where the definition came from: "bundled", "user", or
	// "bundled+user" for a user file merged over a bundled one.
	Source string `toml:"-"`

	// Kept until Task 3 deletes the literal.
	Backend      string `toml:"-"`
	PkgSetup     string `toml:"-"`
	PkgInstall   string `toml:"-"`
	CloudRecipes bool   `toml:"-"`
}

// Pkg is the package-manager surface.
type Pkg struct {
	// Setup refreshes the index. Provision runs it once before the first
	// recipe. Empty means the manager needs no refresh (dnf).
	Setup string `toml:"setup"`
	// Install is argv; apk carries "--wait 60" for the lock race.
	Install []string `toml:"install"`
	// Env is exported in the prelude (DEBIAN_FRONTEND).
	Env map[string]string `toml:"env"`
	// ScaffoldSetup and ScaffoldInstall are display text for `recipe new`.
	ScaffoldSetup   string `toml:"scaffold_setup"`
	ScaffoldInstall string `toml:"scaffold_install"`
	// RuntimePackages maps a recipe runtime to the package that provides it.
	RuntimePackages map[string]string `toml:"runtime_packages"`
}

// Svc is the service surface. Each value is a template: {name} renders to
// the first argument, and a template without {name} gets "$@" appended.
type Svc struct {
	Enable  string `toml:"enable"`
	Start   string `toml:"start"`
	Stop    string `toml:"stop"`
	Restart string `toml:"restart"`
	Status  string `toml:"status"`
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
