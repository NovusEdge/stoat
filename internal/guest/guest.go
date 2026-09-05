// Package guest owns every fact stoat knows about a guest operating system:
// the login shell for a seeded account, packages the base seed assumes but
// an image may not ship, the provisioning backend, the interactive
// installer's name, the default ssh user, the package-manager commands a
// scaffolded recipe uses, and the filename hints that recognise the OS in a
// bring-your-own image.
//
// Adding an OS means adding one guest.toml, bundled or dropped in
// ~/.stoat/guests/. validate rejects a file missing a required field: a
// missing field otherwise fails silently, as an unselectable catalog entry,
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

	// LogPath is where the init system writes its own log, for a guest whose
	// init has no journal. tail_log reads it when the caller names neither a
	// unit nor a path. It is optional: validate does not require it, and a
	// systemd guest leaves it empty because journalctl answers instead.
	LogPath string `toml:"log_path"`

	// Backends holds one opaque table per backend name. The backend package
	// that owns the name decodes it; this package never reads inside.
	Backends map[string]map[string]any `toml:"backend"`

	// Source is where the definition came from: "bundled", "user", or
	// "bundled+user" for a user file merged over a bundled one.
	Source string `toml:"-"`
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

// Get returns one [svc] template by action name, or "" when the guest
// declares none.
func (s Svc) Get(action string) string {
	switch action {
	case "enable":
		return s.Enable
	case "start":
		return s.Start
	case "stop":
		return s.Stop
	case "restart":
		return s.Restart
	case "status":
		return s.Status
	}
	return ""
}

// loaded is the active set: bundled at init, then Load merges user files
// over it. Lookup before Load sees bundled guests only, so a broken user
// file cannot take the bundled set down.
var loaded = loadBundled()

// Lookup returns the OS declared for name. It never guesses: an unknown OS
// is reported as unknown, and it is the caller's job to decide what that
// means.
func Lookup(name string) (OS, bool) {
	o, ok := loaded[name]
	return o, ok
}

// All returns every loaded OS, sorted by name.
func All() []OS {
	names := sortedNames(loaded)
	out := make([]OS, len(names))
	for i, n := range names {
		out[i] = loaded[n]
	}
	return out
}

// Names returns every loaded name, sorted.
func Names() []string { return sortedNames(loaded) }
