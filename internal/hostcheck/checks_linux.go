//go:build linux

package hostcheck

// RunChecks probes every Linux host requirement, in the order they are
// displayed. None of these block the install: qemu.Start gates VM start at
// runtime, so the installer's job is to report missing requirements early.
func RunChecks(d Distro) []Check {
	checks := make([]Check, 0, len(binChecks)+1)
	for _, b := range binChecks {
		c := lookPathCheck(b.name, d.InstallCmd(b.pkg))
		c.Optional = b.optional
		checks = append(checks, c)
	}
	return append(checks, KVMCheck())
}
