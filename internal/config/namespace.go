package config

import (
	"os"
	"path/filepath"
)

// v2Dir is where a VM whose provider is not the local hypervisor lives.
//
// A stoat built before the provider field existed decodes an unknown key with
// a warning and carries on, so it would read a gce VM as a local one, report
// it stopped, and delete its record on `stoat rm` while the cloud instance
// kept billing. Load joins the data root with a VM name and never descends,
// and enumeration skips a directory with no vm.toml at its top, so nesting
// these records one level down is what an old binary cannot open.
const v2Dir = "v2"

// DirFor is where name's vm.toml lives. An existing flat record keeps its
// location forever: this work moves no VM.
func DirFor(name string) string {
	flat := filepath.Join(Root(), name)
	if _, err := os.Stat(filepath.Join(flat, "vm.toml")); err == nil {
		return flat
	}
	return filepath.Join(Root(), v2Dir, name)
}

// Exists reports whether name is taken in either namespace. VM.WorkDir keys
// on the directory's base name, so v2/dev and dev would share one workspace.
func Exists(name string) bool {
	for _, d := range []string{filepath.Join(Root(), name), filepath.Join(Root(), v2Dir, name)} {
		if _, err := os.Stat(filepath.Join(d, "vm.toml")); err == nil {
			return true
		}
	}
	return false
}

// v2Names lists the VM directories under v2, or nothing when it is absent.
func v2Names() []string {
	entries, err := os.ReadDir(filepath.Join(Root(), v2Dir))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

// ProviderOf reports name's provider from where its record lives, without
// requiring vm.toml to parse. A flat record is qemu by definition (Save
// never routes qemu there otherwise); a v2 record's provider is read from
// vm.toml when it parses, and falls back to "unknown" when it does not, so a
// broken cloud record is never mistaken for a local one.
func ProviderOf(name string) (string, error) {
	flat := filepath.Join(Root(), name)
	if _, err := os.Stat(filepath.Join(flat, "vm.toml")); err == nil {
		return "qemu", nil
	}
	dir := filepath.Join(Root(), v2Dir, name)
	if _, err := os.Stat(filepath.Join(dir, "vm.toml")); err != nil {
		return "", err
	}
	if v, err := Load(name); err == nil {
		if v.Provider == "" {
			return "unknown", nil
		}
		return v.Provider, nil
	}
	return "unknown", nil
}
