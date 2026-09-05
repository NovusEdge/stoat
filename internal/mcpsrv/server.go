package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// class is a tool's annotation class. The four classes are the taxonomy in
// docs/design/mcp-server.md; toolTable in table_test.go assigns one per tool.
type class int

const (
	classRead class = iota
	classMutate
	classDestructive
	classExec
)

type toolSpec struct {
	Name   string
	Class  class
	Access Level
}

// Options configures a server. Version is the stoat build version, reported
// in the MCP handshake.
type Options struct {
	Version string
	Limits  Limits
}

const maxLogLines = 2000

// New is a stub; Task 5's implementer registers the tool set and Task 6
// wires the rate limit middleware.
func New(opts Options) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{Name: "stoat", Version: opts.Version}, nil)
}

// annotationsFor is a stub; server_test.go pins the real mapping from class.
func annotationsFor(c class) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{}
}

// clampInt is a stub; server_test.go pins the real clamp.
func clampInt(v, lo, hi int) int {
	return 0
}
