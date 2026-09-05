package mcpsrv

import (
	"errors"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// InstallOpts controls where Install writes a client's config entry.
type InstallOpts struct {
	Project bool
	Print   bool
}

// Install writes the named MCP client's config entry so it launches this
// binary. The client config formats (claude-code, claude-desktop, cursor,
// vscode) are not implemented yet.
func Install(client string, opts InstallOpts) (wire.MCPInstall, error) {
	return wire.MCPInstall{}, errors.New("mcp install: not implemented yet")
}
