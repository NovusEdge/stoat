package cli

import (
	"fmt"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/project"
)

// vmCommands are the commands whose single positional is a VM name. Only
// these get their argument resolved against stoat.toml. pull takes an image
// id and `recipe new` takes a recipe name in the same Args.VM slot, so a
// blanket rewrite would send both to the wrong place.
// "provision" is absent on purpose: the guest-definitions plan made it a kong
// alias of apply, so Args.Cmd never holds it.
var vmCommands = map[string]bool{
	"get": true, "up": true, "down": true, "ssh": true, "ssh-command": true,
	"rm": true, "clone": true, "exec": true, "cp": true, "forward": true,
	"snapshot": true, "wait": true, "apply": true,
	"update": true, "logs": true, "capabilities": true,
}

// fanOutCommands are the commands that act on every declared VM when given no
// argument at project scope.
var fanOutCommands = map[string]bool{
	"up": true, "down": true, "apply": true, "wait": true, "rm": true,
}

// resolveScope loads the project in the current directory, if any, and turns
// a bare VM argument into a global name.
//
// It runs after Parse, not inside it: Parse never touches disk, which is what
// makes it testable as a pure function over argv.
func resolveScope(a *Args) error {
	p, ok, err := project.Find()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	a.Project = p

	if a.VM == "" || !vmCommands[a.Cmd] {
		return nil
	}
	if global, ok := p.Resolve(a.VM); ok {
		a.VM = global
		return nil
	}
	// The name may still be a global VM that this project never declared;
	// only refuse when the data root does not have it either.
	if a.Cmd == "capabilities" {
		if _, err := capabilities.LoadTarget(a.VM); err != nil {
			return err
		}
		return nil
	}
	if _, err := coreGet(a.VM); err != nil {
		return fmt.Errorf("no VM %q in %s or ~/.stoat/vms", a.VM, project.FileName)
	}
	return nil
}
