package capabilities

import (
	"runtime"

	"github.com/novusedge/stoat/internal/core"
)

// Build evaluates the supplied host and metadata observations without I/O.
func Build(in Input) Report {
	report := Report{
		Schema:       1,
		StoatVersion: in.Version,
		Host:         Host{OS: runtime.GOOS, Arch: runtime.GOARCH, ProjectState: in.ProjectState},
		AccessPolicy: AccessPolicy{
			MCPAgentAccessEnforced: true,
			CLIAgentAccessEnforced: false,
			CLICommands:            []string{"exec", "cp"},
		},
		Profiles:     []Profile{},
		Capabilities: []Capability{},
		Unavailable:  []Capability{},
	}
	if in.Target != nil {
		target := *in.Target
		report.Target = &target
	}

	report.Profiles = append(report.Profiles, qemuProfile(in.HostChecks))
	report.Profiles = append(report.Profiles,
		Profile{Name: "recipe-sh", Status: StatusSupported, Scope: ScopeRuntime,
			Requirements: []Requirement{}, Limits: []Limit{}, Evidence: []Evidence{implementationEvidence("recipe.runtime.sh")}},
		Profile{Name: "recipe-python3", Status: StatusSupported, Scope: ScopeRuntime,
			Requirements: []Requirement{requirement("guest_runtime", "python3", "")}, Limits: []Limit{}, Evidence: []Evidence{implementationEvidence("recipe.runtime.python3")}},
	)

	add := func(entry Capability) { report.Capabilities = append(report.Capabilities, entry) }
	add(currentEntry("vm.lifecycle", StatusSupported, ScopeVM, targetRequirement(), nil, nil))

	snapshot := currentEntry("vm.snapshot", StatusSupported, ScopeVM, targetRequirement(), nil, nil)
	if in.Target != nil {
		snapshot.Evidence = append(snapshot.Evidence, configEvidence("vm.mode", in.Target.Mode))
		switch in.Target.Mode {
		case "live":
			snapshot.Status = StatusLimited
			snapshot.Limits = append(snapshot.Limits, Limit{Code: LimitDiskRequired})
		case "disk", "cloud":
		case "":
			snapshot.Status = StatusUnknown
			snapshot.Reason = &Reason{Code: ReasonTargetModeUnknown}
		default:
			snapshot.Status = StatusUnknown
			snapshot.Reason = &Reason{Code: ReasonTargetModeUnknown}
		}
	}
	add(snapshot)

	add(currentEntry("recipes", StatusSupported, ScopeVM, targetRequirement(), nil, nil))

	project := currentEntry("project.operations", StatusSupported, ScopeProject, nil, nil, nil)
	project.Evidence = append(project.Evidence, configEvidence("project.scope", in.ProjectState))
	switch in.ProjectState {
	case "available":
	case "absent":
		project.Status = StatusLimited
		project.Limits = append(project.Limits, Limit{Code: LimitProjectFileRequired})
	case "unknown", "":
		project.Status = StatusUnknown
		project.Reason = &Reason{Code: ReasonProjectStateUnknown}
	}
	add(project)

	for _, name := range []string{"mcp.guest.observe", "mcp.guest.manage", "mcp.guest.exec"} {
		need := name[len("mcp.guest."):]
		entry := currentEntry(name, StatusSupported, ScopeVM, targetRequirement(), nil, nil)
		entry.Requirements = append(entry.Requirements, requirement("mcp_agent_access", "agent_access", need))
		if in.Target != nil {
			entry.Evidence = append(entry.Evidence, configEvidence("vm.agent_access", in.Target.AgentAccess))
			have, ok := accessRank[in.Target.AgentAccess]
			want := accessRank[need]
			if !ok {
				entry.Status = StatusUnknown
				entry.Reason = &Reason{Code: ReasonAgentAccessUnknown}
			} else if have < want {
				entry.Status = StatusLimited
				entry.Limits = append(entry.Limits, Limit{Code: LimitAgentAccessRequired, Value: need})
			}
		}
		add(entry)
	}

	cli := currentEntry("cli.guest.shell", StatusSupported, ScopeCLI, targetRequirement(), nil, nil)
	add(cli)
	add(currentEntry("host.diagnostics", StatusSupported, ScopeHost, nil, nil, nil))

	for _, name := range []string{"runtime.fork", "runtime.continuation"} {
		report.Unavailable = append(report.Unavailable, Capability{
			Name: name, Status: StatusUnsupported, Scope: ScopeRuntime,
			Requirements: []Requirement{}, Limits: []Limit{}, Reason: &Reason{Code: ReasonNotImplemented},
			Evidence: []Evidence{{Kind: "implementation", Source: name, Result: ReasonNotImplemented}},
		})
	}
	return report
}

var accessRank = map[string]int{"none": 0, "observe": 1, "manage": 2, "exec": 3}

func requirement(kind, name, value string) Requirement {
	return Requirement{Kind: kind, Name: name, Value: value}
}

func targetRequirement() []Requirement {
	return []Requirement{{Kind: "target", Name: "vm"}}
}

func implementationEvidence(source string) Evidence {
	return Evidence{Kind: "implementation", Source: source, Result: "implemented"}
}

func configEvidence(source, result string) Evidence {
	return Evidence{Kind: "config", Source: source, Result: result}
}

func currentEntry(name, status, scope string, requirements []Requirement, limits []Limit, reason *Reason) Capability {
	if requirements == nil {
		requirements = []Requirement{}
	}
	if limits == nil {
		limits = []Limit{}
	}
	return Capability{Name: name, Status: status, Scope: scope, Requirements: requirements, Limits: limits, Reason: reason, Evidence: []Evidence{implementationEvidence(name)}}
}

func qemuProfile(checks []core.HostCheck) Profile {
	requirements := []Requirement{
		requirement("host_tool", "qemu-system-x86_64", ""),
		requirement("host_tool", "qemu-img", ""),
		requirement("host_device", "/dev/kvm", ""),
	}
	p := Profile{Name: "qemu-x86_64", Status: StatusUnknown, Scope: ScopeHost, Requirements: requirements, Limits: []Limit{}, Evidence: []Evidence{}}
	byName := make(map[string]core.HostCheck, len(checks))
	for _, c := range checks {
		if _, exists := byName[c.Name]; !exists {
			byName[c.Name] = c
		}
	}
	missing := false
	for _, req := range requirements {
		c, ok := byName[req.Name]
		if !ok {
			missing = true
			continue
		}
		result := "failed"
		if c.OK {
			result = "available"
		}
		p.Evidence = append(p.Evidence, Evidence{Kind: "host_check", Source: c.Name, Result: result})
	}
	if missing {
		p.Reason = &Reason{Code: ReasonHostProbeUnavailable}
		return p
	}
	p.Status = StatusSupported
	for _, req := range requirements {
		if c := byName[req.Name]; !c.OK {
			p.Status = StatusLimited
			p.Limits = append(p.Limits, Limit{Code: LimitHostRequirementMissing, Value: req.Name})
		}
	}
	return p
}
