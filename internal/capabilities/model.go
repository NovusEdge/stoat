package capabilities

import "github.com/novusedge/stoat/internal/core"

// Report is the versioned capability discovery document shared by the CLI and
// MCP adapters.
type Report struct {
	Schema       int          `json:"schema"`
	StoatVersion string       `json:"stoat_version"`
	Host         Host         `json:"host"`
	Target       *Target      `json:"target,omitempty"`
	AccessPolicy AccessPolicy `json:"access_policy"`
	Profiles     []Profile    `json:"profiles"`
	Capabilities []Capability `json:"capabilities"`
	Unavailable  []Capability `json:"unavailable"`
}

type Host struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	ProjectState string `json:"project_state"`
}

type Target struct {
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	AgentAccess string `json:"agent_access"`
}

type AccessPolicy struct {
	MCPAgentAccessEnforced bool     `json:"mcp_agent_access_enforced"`
	CLIAgentAccessEnforced bool     `json:"cli_agent_access_enforced"`
	CLICommands            []string `json:"cli_commands"`
}

type Profile struct {
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	Scope        string        `json:"scope"`
	Requirements []Requirement `json:"requirements"`
	Limits       []Limit       `json:"limits"`
	Reason       *Reason       `json:"reason,omitempty"`
	Evidence     []Evidence    `json:"evidence"`
}

type Capability struct {
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	Scope        string        `json:"scope"`
	Requirements []Requirement `json:"requirements"`
	Limits       []Limit       `json:"limits"`
	Reason       *Reason       `json:"reason,omitempty"`
	Evidence     []Evidence    `json:"evidence"`
}

type Requirement struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type Limit struct {
	Code  string `json:"code"`
	Value string `json:"value,omitempty"`
}

type Reason struct {
	Code string `json:"code"`
}

type Evidence struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Result string `json:"result"`
}

type Input struct {
	Version      string
	ProjectState string
	HostChecks   []core.HostCheck
	Target       *Target
}
