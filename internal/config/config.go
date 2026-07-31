// Package config owns vm.toml files under the stoat data root.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// VM is one virtual machine. vm.toml is authoritative; there is no cache.
type VM struct {
	Name      string   `toml:"name"`
	Mode      string   `toml:"mode"` // "live" | "disk" | "cloud"
	OS        string   `toml:"os"`
	ISO       string   `toml:"iso"` // relative to the data root
	RAM       int      `toml:"ram"` // MB
	CPUs      int      `toml:"cpus"`
	Disk      string   `toml:"disk"`      // disk mode only, e.g. "8G"
	Installed bool     `toml:"installed"` // disk mode only; flips boot order
	Share     string   `toml:"share"`     // host dir exposed as /mnt/host
	SSHPort   int      `toml:"sshport"`
	Recipes   []string `toml:"recipes"`

	// Backend is the provisioning backend: "apkovl" | "cloudinit" | "ssh".
	// Written by the form at creation time; dispatch elsewhere in stoat
	// keys off Mode, not this field.
	Backend string `toml:"backend"`
	// Base is the absolute path to the shared base image an overlay is
	// created from. Cloud mode only.
	Base string `toml:"base"`
	// SSHUser is the account used for SSH-based provisioning/access. Empty
	// means root; callers apply that default themselves rather than this
	// package writing "root" into every vm.toml.
	SSHUser string `toml:"sshuser"`

	// ConsolePassword is the password for SSHUser at the VM's GRAPHICAL
	// CONSOLE — the qemu window — not over ssh. Cloud images lock every
	// account by default (cloud-init's lock_passwd defaults to true) and
	// stoat's seed sets ssh_pwauth: false, so without this a cloud VM shows
	// a login prompt nobody on earth can answer: root is locked and the
	// stoat user has no password. Empty means no console login is possible,
	// which is the correct state for a live Alpine VM, whose root already
	// logs in at the console with no password at all.
	ConsolePassword string `toml:"console_password"`

	Dir string `toml:"-"` // absolute path to the VM directory
}

// Root is the data root: $STOAT_HOME, or ~/.stoat.
func Root() string {
	if r := os.Getenv("STOAT_HOME"); r != "" {
		return r
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".stoat"
	}
	return filepath.Join(home, ".stoat")
}

// EnsureRoot creates the data root and its fixed subdirectories.
func EnsureRoot() error {
	for _, d := range []string{"isos", "recipes"} {
		if err := os.MkdirAll(filepath.Join(Root(), d), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// expand resolves a leading ~ against the user's home directory.
func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func (v *VM) path() string        { return filepath.Join(v.Dir, "vm.toml") }
func (v *VM) DiskPath() string    { return filepath.Join(v.Dir, "disk.qcow2") }
func (v *VM) PidPath() string     { return filepath.Join(v.Dir, "qemu.pid") }
func (v *VM) MonitorPath() string { return filepath.Join(v.Dir, "monitor.sock") }
func (v *VM) OvlDir() string      { return filepath.Join(v.Dir, "ovl") }

// ISOPath resolves the configured ISO against the data root.
func (v *VM) ISOPath() string {
	if filepath.IsAbs(v.ISO) {
		return v.ISO
	}
	return filepath.Join(Root(), v.ISO)
}

// Save writes vm.toml, creating the VM directory if needed.
func (v *VM) Save() error {
	if v.Dir == "" {
		v.Dir = filepath.Join(Root(), v.Name)
	}
	if err := os.MkdirAll(v.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(v.path())
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(v)
}

// Load reads one VM by name.
func Load(name string) (*VM, error) {
	dir := filepath.Join(Root(), name)
	v := &VM{}
	if _, err := toml.DecodeFile(filepath.Join(dir, "vm.toml"), v); err != nil {
		return nil, err
	}
	v.Dir = dir
	v.Share = expand(v.Share)
	return v, nil
}

// List returns every VM in the data root, sorted by name.
func List() ([]*VM, error) {
	entries, err := os.ReadDir(Root())
	if err != nil {
		return nil, err
	}
	var vms []*VM
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "isos" || e.Name() == "recipes" {
			continue
		}
		v, err := Load(e.Name())
		if err != nil {
			continue // not a VM directory
		}
		vms = append(vms, v)
	}
	sort.Slice(vms, func(i, j int) bool { return vms[i].Name < vms[j].Name })
	return vms, nil
}

// Broken describes a VM directory whose vm.toml exists but failed to parse.
// A directory with no vm.toml at all is not a VM and is never reported here.
type Broken struct {
	Name string
	Err  error
}

// ListBroken returns every VM directory whose vm.toml exists but fails to
// parse, sorted by name. It is the counterpart to List: List silently omits
// these directories (a broken vm.toml cannot yield a usable *VM), so callers
// that want to tell the user "this VM is broken" rather than making it look
// deleted must call ListBroken separately.
func ListBroken() ([]Broken, error) {
	entries, err := os.ReadDir(Root())
	if err != nil {
		return nil, err
	}
	var broken []Broken
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "isos" || e.Name() == "recipes" {
			continue
		}
		if _, err := os.Stat(filepath.Join(Root(), e.Name(), "vm.toml")); err != nil {
			continue // no vm.toml: not a VM directory
		}
		if _, err := Load(e.Name()); err != nil {
			broken = append(broken, Broken{Name: e.Name(), Err: err})
		}
	}
	sort.Slice(broken, func(i, j int) bool { return broken[i].Name < broken[j].Name })
	return broken, nil
}

// sshPortLine is a best-effort match for a `sshport = N` line in a vm.toml
// that otherwise fails to parse (e.g. an unterminated string earlier in the
// file). Used by FreePort so a broken VM's port isn't handed out again.
var sshPortLine = regexp.MustCompile(`(?m)^\s*sshport\s*=\s*(\d+)\s*$`)

// Delete removes the VM directory. It never touches isos/.
func (v *VM) Delete() error {
	if v.Dir == "" || filepath.Dir(v.Dir) != Root() {
		return fmt.Errorf("refusing to delete %q: outside the data root", v.Dir)
	}
	return os.RemoveAll(v.Dir)
}

// BrokenSSHPort best-effort reads the sshport out of a VM directory whose
// vm.toml does not parse, so callers assigning a port can avoid one that a
// broken VM is still committed to. An error means no port could be read —
// which is not the same as the VM claiming none.
func BrokenSSHPort(name string) (int, error) {
	data, err := os.ReadFile(filepath.Join(Root(), name, "vm.toml"))
	if err != nil {
		return 0, err
	}
	m := sshPortLine.FindSubmatch(data)
	if m == nil {
		return 0, fmt.Errorf("%s: no sshport line", name)
	}
	return strconv.Atoi(string(m[1]))
}

// FreePort returns the first free TCP port at or above 2200 on loopback that
// is both bindable right now and not already claimed by an existing VM's
// vm.toml. A created-but-stopped VM holds nothing open, so the bindability
// check alone is not enough to avoid handing out the same port twice in a
// row.
func FreePort() (int, error) {
	claimed := map[int]bool{}
	if vms, err := List(); err == nil {
		// An unreadable/missing data root (fresh install) means no VMs are
		// claimed; List's error is deliberately ignored here.
		for _, v := range vms {
			claimed[v.SSHPort] = true
		}
	}
	// A broken vm.toml can't be parsed for its port, but the port it was
	// using is very likely still committed to that VM's disk image. Rather
	// than treat "unparseable" as "claims nothing" (which is exactly how the
	// original collision happened), best-effort regex the raw file text for
	// a sshport line and reserve that port too.
	if broken, err := ListBroken(); err == nil {
		for _, b := range broken {
			if p, err := BrokenSSHPort(b.Name); err == nil {
				claimed[p] = true
			}
		}
	}
	for p := 2200; p < 2300; p++ {
		if claimed[p] {
			continue
		}
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		l.Close()
		return p, nil
	}
	return 0, fmt.Errorf("no free port in 2200-2299")
}
