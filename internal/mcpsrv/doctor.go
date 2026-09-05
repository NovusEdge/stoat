package mcpsrv

import "github.com/novusedge/stoat/internal/cli/wire"

// DoctorReport says what this server is and whether each client's config
// entry points at the binary that is running.
func DoctorReport(version string) wire.MCPDoctor {
	return wire.MCPDoctor{}
}
