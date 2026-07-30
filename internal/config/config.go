// Package config owns vm.toml files under the stoat data root.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// VM is one virtual machine. vm.toml is authoritative; there is no cache.
type VM struct {
	Name      string   `toml:"name"`
	Mode      string   `toml:"mode"` // "live" | "disk"
	ISO       string   `toml:"iso"`  // relative to the data root
	RAM       int      `toml:"ram"`  // MB
	CPUs      int      `toml:"cpus"`
	Disk      string   `toml:"disk"`      // disk mode only, e.g. "8G"
	Installed bool     `toml:"installed"` // disk mode only; flips boot order
	Share     string   `toml:"share"`     // host dir exposed as /mnt/host
	SSHPort   int      `toml:"sshport"`
	Recipes   []string `toml:"recipes"`

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

// Delete removes the VM directory. It never touches isos/.
func (v *VM) Delete() error {
	if v.Dir == "" || filepath.Dir(v.Dir) != Root() {
		return fmt.Errorf("refusing to delete %q: outside the data root", v.Dir)
	}
	return os.RemoveAll(v.Dir)
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
