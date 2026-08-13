package core

import "github.com/novusedge/stoat/internal/config"

// NeedsProvision reports whether a provision run on v would do any work.
//
// True covers two cases: a recipe filterByRunMode would still run (never
// applied, or applied against a script that has since changed), or v is a
// disk VM with a share set, since sshx.Provision's share-mount step is
// idempotent but leaves no Applied entry to check.
//
// For cloud VMs, this may return true on first boot (before discoverCloudInitApplied
// has populated v.Applied). Apply handles that: it reads the markers cloud-init
// left, populates Applied, and skips recipes already run. A minor no-op pass
// on first boot is acceptable; it's the price of supporting recipes added
// after creation.
func NeedsProvision(v *config.VM) (bool, error) {
	runTargets, _, err := filterByRunMode(v, v.Recipes, nil)
	if err != nil {
		return false, err
	}
	if len(runTargets) > 0 {
		return true, nil
	}
	return (v.Mode == "disk" || v.Mode == "cloud") && v.Share != "", nil
}
