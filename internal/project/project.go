// Package project owns stoat.toml: the VMs, recipes and params a repository
// declares. It reads the file and answers name questions about it. It runs
// nothing and touches no VM; internal/core does that.
package project

import "errors"

// FileName is the declaration file. Its presence in os.Getwd() is the whole
// project-scope test; see Find.
const FileName = "stoat.toml"

// CacheDir holds the recipe cache and the secrets file.
const CacheDir = ".stoat"

// VM is one [vms.<key>] declaration.
type VM struct {
	Key string

	Name        string
	Image       string
	CPUs        int
	RAM         int
	Disk        string
	Recipes     []string
	Shares      []string
	AgentAccess string
	Params      map[string]map[string]any
}

// Project is one loaded stoat.toml.
type Project struct {
	Dir  string
	Name string
	VMs  []VM

	Recipes map[string]any
}

// stubErr marks every entry point below as not yet implemented.
var stubErr = errors.New("project: not implemented")

// Load reads dir/stoat.toml.
func Load(dir string) (*Project, error) {
	return nil, stubErr
}

// Find loads the project in the current directory, if there is one.
func Find() (*Project, bool, error) {
	return nil, false, stubErr
}

// GlobalName is the VM's directory name under the data root.
func (p *Project) GlobalName(key string) string {
	return ""
}

// Resolve turns a bare command argument into a global VM name.
func (p *Project) Resolve(arg string) (string, bool) {
	return "", false
}

// VM returns one declaration by key.
func (p *Project) VM(key string) (VM, bool) {
	return VM{}, false
}

// KeyFor is Resolve backwards.
func (p *Project) KeyFor(global string) (string, bool) {
	return "", false
}
