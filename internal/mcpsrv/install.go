package mcpsrv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// clientConfig is where one MCP client keeps its server list. VS Code uses
// "servers"; the others use "mcpServers".
type clientConfig struct {
	Client  string
	Key     string
	Rel     string // relative to home, or to the working directory when Local
	Local   bool
	Project string // the path --project writes instead, relative to the working directory
}

var clients = []clientConfig{
	{Client: "claude-code", Key: "mcpServers", Rel: ".claude.json", Project: ".mcp.json"},
	{Client: "claude-desktop", Key: "mcpServers", Rel: ".config/Claude/claude_desktop_config.json"},
	{Client: "cursor", Key: "mcpServers", Rel: ".cursor/mcp.json"},
	{Client: "vscode", Key: "servers", Rel: ".vscode/mcp.json", Local: true},
}

// InstallOpts configures where Install writes the client's server entry.
type InstallOpts struct {
	Project bool
	Print   bool
}

// entry is the server record every client understands. cwd is written so
// project scope applies to the server the client launches.
type entry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	CWD     string   `json:"cwd"`
}

func configFor(client string) (clientConfig, error) {
	i := slices.IndexFunc(clients, func(c clientConfig) bool { return c.Client == client })
	if i < 0 {
		names := make([]string, len(clients))
		for j, c := range clients {
			names[j] = c.Client
		}
		return clientConfig{}, fmt.Errorf("unknown client %q: one of %v", client, names)
	}
	return clients[i], nil
}

func configPath(c clientConfig, opts InstallOpts) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if c.Local {
		return filepath.Join(cwd, c.Rel), nil
	}
	if opts.Project && c.Project != "" {
		return filepath.Join(cwd, c.Project), nil
	}
	if opts.Project {
		return "", fmt.Errorf("%s has no project scoped config; drop --project", c.Client)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, c.Rel), nil
}

// Install writes the named client's MCP server entry for this binary. It
// replaces an existing stoat entry, leaves every other entry and every
// other top-level key alone, and writes through a temp file and a rename.
func Install(client string, opts InstallOpts) (wire.MCPInstall, error) {
	c, err := configFor(client)
	if err != nil {
		return wire.MCPInstall{}, err
	}
	path, err := configPath(c, opts)
	if err != nil {
		return wire.MCPInstall{}, err
	}
	bin, err := os.Executable()
	if err != nil {
		return wire.MCPInstall{}, err
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return wire.MCPInstall{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return wire.MCPInstall{}, err
	}
	e := entry{Command: bin, Args: []string{"mcp"}, CWD: cwd}

	pretty, err := json.MarshalIndent(map[string]any{c.Key: map[string]any{"stoat": e}}, "", "  ")
	if err != nil {
		return wire.MCPInstall{}, err
	}
	report := wire.MCPInstall{Client: c.Client, Path: path, JSON: string(pretty)}
	if opts.Print {
		report.Path = ""
		return report, nil
	}

	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return wire.MCPInstall{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	servers, _ := doc[c.Key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["stoat"] = e
	doc[c.Key] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return wire.MCPInstall{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return wire.MCPInstall{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-*.json")
	if err != nil {
		return wire.MCPInstall{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		return wire.MCPInstall{}, err
	}
	if err := tmp.Close(); err != nil {
		return wire.MCPInstall{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return wire.MCPInstall{}, err
	}
	return report, nil
}
