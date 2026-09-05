package mcpsrv

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// DoctorReport says what this server is and whether each client's config
// entry points at the binary that is running. A stale entry launches a
// different stoat than the one the user just installed, and that is the
// failure this command exists to name.
func DoctorReport(version string) wire.MCPDoctor {
	bin, _ := os.Executable()
	bin, _ = filepath.Abs(bin)
	r := wire.MCPDoctor{
		Contract:  Contract,
		Version:   version,
		Transport: "stdio",
		Binary:    bin,
		Clients:   []wire.MCPClient{},
	}
	for _, c := range clients {
		row := wire.MCPClient{Client: c.Client}
		path, err := configPath(c, InstallOpts{})
		if err != nil {
			r.Clients = append(r.Clients, row)
			continue
		}
		row.Path = path
		raw, err := os.ReadFile(path)
		if err != nil {
			r.Clients = append(r.Clients, row)
			continue
		}
		// Only the server map is decoded as entries. ~/.claude.json holds
		// numbers, strings and unrelated objects beside mcpServers, so
		// decoding the whole document as entries fails on the first of them
		// and reports an installed client as missing.
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			r.Clients = append(r.Clients, row)
			continue
		}
		var servers map[string]entry
		if err := json.Unmarshal(doc[c.Key], &servers); err != nil {
			r.Clients = append(r.Clients, row)
			continue
		}
		e, ok := servers["stoat"]
		if !ok {
			r.Clients = append(r.Clients, row)
			continue
		}
		row.Installed = true
		row.Command = e.Command
		row.Current = e.Command == bin
		r.Clients = append(r.Clients, row)
	}
	return r
}
