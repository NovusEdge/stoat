package capabilities

import "github.com/novusedge/stoat/internal/core"

const (
	StatusSupported   = "supported"
	StatusLimited     = "limited"
	StatusUnsupported = "unsupported"
	StatusUnknown     = "unknown"

	ScopeHost    = "host"
	ScopeVM      = "vm"
	ScopeProject = "project"
	ScopeRuntime = "runtime"
	ScopeMCP     = "mcp"
	ScopeCLI     = "cli"

	ReasonNotImplemented       = "not_implemented"
	ReasonHostProbeUnavailable = "host_probe_unavailable"
	ReasonAgentAccessUnknown   = "agent_access_unknown"
	ReasonTargetModeUnknown    = "target_mode_unknown"
	ReasonProjectStateUnknown  = "project_state_unknown"

	LimitAgentAccessRequired    = "agent_access_required"
	LimitTargetRequired         = "target_required"
	LimitDiskRequired           = "disk_required"
	LimitProjectFileRequired    = "project_file_required"
	LimitHostRequirementMissing = "host_requirement_missing"
)

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
