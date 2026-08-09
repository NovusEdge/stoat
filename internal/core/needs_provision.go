package core

import "github.com/novusedge/stoat/internal/config"

// NeedsProvision reports whether a provision run on v would do any work.
//
// True covers two cases: a recipe filterByRunMode would still run (never
// applied, or applied against a script that has since changed), or v is a
// disk VM with a share set, since sshx.Provision's share-mount step is
// idempotent but leaves no Applied entry to check.
//
// A cloud VM always returns false. cloud-init applies its recipes from the
// seed at first boot, so an ssh provision run has nothing to do.
func NeedsProvision(v *config.VM) (bool, error) {
	if v.Mode == "cloud" {
		return false, nil
	}
	runTargets, _, err := filterByRunMode(v, v.Recipes, nil)
	if err != nil {
		return false, err
	}
	if len(runTargets) > 0 {
		return true, nil
	}
	return v.Mode == "disk" && v.Share != "", nil
}
