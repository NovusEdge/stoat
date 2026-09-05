package mcpsrv

import (
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// DoctorReport says what this server is: the contract it speaks, the
// transport `mcp serve` uses by default, and the binary a client would
// launch. Client config detection (wire.MCPDoctor.Clients) arrives with
// Install, which reads the same files this reports on.
func DoctorReport(version string) wire.MCPDoctor {
	bin, err := os.Executable()
	if err == nil {
		if abs, err := filepath.Abs(bin); err == nil {
			bin = abs
		}
	}
	return wire.MCPDoctor{
		Contract:  Contract,
		Version:   version,
		Transport: "stdio",
		Binary:    bin,
		Clients:   []wire.MCPClient{},
	}
}
