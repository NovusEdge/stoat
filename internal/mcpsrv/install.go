package mcpsrv

import "github.com/novusedge/stoat/internal/cli/wire"

// InstallOpts configures where Install writes the client's server entry.
type InstallOpts struct {
	Project bool
	Print   bool
}

// Install writes the named client's MCP server entry for this binary.
func Install(client string, opts InstallOpts) (wire.MCPInstall, error) {
	return wire.MCPInstall{}, nil
}
