package capabilities

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func passingChecks() []core.HostCheck {
	return []core.HostCheck{
		{Name: "qemu-system-x86_64", OK: true},
		{Name: "qemu-img", OK: true},
		{Name: "/dev/kvm", OK: true},
		{Name: "ssh", OK: true},
	}
}

func target(name, mode, access string) *Target {
	return &Target{Name: name, Mode: mode, AgentAccess: access}
}

func reportEntry(t *testing.T, entries []Capability, name string) Capability {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("report omitted %q", name)
	return Capability{}
}

func profileEntry(t *testing.T, entries []Profile, name string) Profile {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("report omitted profile %q", name)
	return Profile{}
}

func TestBuildReportsProfilesAndTargetPolicy(t *testing.T) {
	report := Build(Input{
		Version:      "v-test",
		ProjectState: "absent",
		HostChecks:   passingChecks(),
		Target:       target("work", "cloud", "observe"),
	})

	if report.Schema != 1 {
		t.Errorf("Schema = %d, want 1", report.Schema)
	}
	if report.StoatVersion != "v-test" {
		t.Errorf("StoatVersion = %q, want v-test", report.StoatVersion)
	}
	if report.Target == nil {
		t.Fatal("Target is nil, want stored target snapshot")
	}
	if *report.Target != *target("work", "cloud", "observe") {
		t.Errorf("Target = %+v, want work/cloud/observe", *report.Target)
	}
	if !report.AccessPolicy.MCPAgentAccessEnforced {
		t.Error("MCPAgentAccessEnforced = false, want true")
	}
	if report.AccessPolicy.CLIAgentAccessEnforced {
		t.Error("CLIAgentAccessEnforced = true, want false")
	}
	if !slices.Equal(report.AccessPolicy.CLICommands, []string{"exec", "cp"}) {
		t.Errorf("CLICommands = %v, want [exec cp]", report.AccessPolicy.CLICommands)
	}
	if report.Profiles == nil || report.Capabilities == nil || report.Unavailable == nil {
		t.Fatalf("report lists must be non-nil: profiles=%v capabilities=%v unavailable=%v", report.Profiles, report.Capabilities, report.Unavailable)
	}
	for _, entry := range report.Profiles {
		if entry.Requirements == nil || entry.Limits == nil || entry.Evidence == nil {
			t.Errorf("profile %q has nil list: %+v", entry.Name, entry)
		}
	}
	for _, entry := range append(slices.Clone(report.Capabilities), report.Unavailable...) {
		if entry.Requirements == nil || entry.Limits == nil || entry.Evidence == nil {
			t.Errorf("entry %q has nil list: %+v", entry.Name, entry)
		}
	}

	for _, want := range []struct {
		name, status, limit string
	}{
		{name: "mcp.guest.observe", status: "supported"},
		{name: "mcp.guest.manage", status: "limited", limit: "manage"},
		{name: "mcp.guest.exec", status: "limited", limit: "exec"},
	} {
		entry := reportEntry(t, report.Capabilities, want.name)
		if entry.Status != want.status {
			t.Errorf("%s status = %q, want %q", want.name, entry.Status, want.status)
		}
		if want.limit != "" {
			if len(entry.Limits) != 1 || entry.Limits[0].Code != "agent_access_required" || entry.Limits[0].Value != want.limit {
				t.Errorf("%s limits = %+v, want agent_access_required=%s", want.name, entry.Limits, want.limit)
			}
		}
	}
	snapshot := reportEntry(t, report.Capabilities, "vm.snapshot")
	if snapshot.Status != "supported" {
		t.Errorf("vm.snapshot status = %q, want supported for cloud target", snapshot.Status)
	}
}

func TestBuildKeepsRuntimeProposalsUnavailable(t *testing.T) {
	report := Build(Input{Version: "v-test", HostChecks: passingChecks()})
	if len(report.Unavailable) != 2 {
		t.Fatalf("Unavailable has %d entries, want exactly 2: %+v", len(report.Unavailable), report.Unavailable)
	}
	for _, name := range []string{"runtime.fork", "runtime.continuation"} {
		entry := reportEntry(t, report.Unavailable, name)
		if entry.Status != "unsupported" {
			t.Errorf("%s status = %q, want unsupported", name, entry.Status)
		}
		if entry.Reason == nil || entry.Reason.Code != "not_implemented" {
			t.Errorf("%s reason = %+v, want not_implemented", name, entry.Reason)
		}
		for _, current := range report.Capabilities {
			if current.Name == name {
				t.Errorf("%s appears in current capabilities", name)
			}
		}
	}
}

func TestCapabilitiesBoundaries(t *testing.T) {
	t.Run("access levels follow ordered MCP policy", func(t *testing.T) {
		for _, tc := range []struct {
			access string
			status [3]string
		}{
			{access: "none", status: [3]string{"limited", "limited", "limited"}},
			{access: "observe", status: [3]string{"supported", "limited", "limited"}},
			{access: "manage", status: [3]string{"supported", "supported", "limited"}},
			{access: "exec", status: [3]string{"supported", "supported", "supported"}},
		} {
			t.Run(tc.access, func(t *testing.T) {
				report := Build(Input{HostChecks: passingChecks(), Target: target("work", "cloud", tc.access)})
				for i, name := range []string{"mcp.guest.observe", "mcp.guest.manage", "mcp.guest.exec"} {
					if got := reportEntry(t, report.Capabilities, name).Status; got != tc.status[i] {
						t.Errorf("%s status = %q, want %q", name, got, tc.status[i])
					}
				}
			})
		}
	})

	t.Run("live target has disk limit", func(t *testing.T) {
		report := Build(Input{HostChecks: passingChecks(), Target: target("work", "live", "observe")})
		entry := reportEntry(t, report.Capabilities, "vm.snapshot")
		if entry.Status != "limited" || len(entry.Limits) != 1 || entry.Limits[0].Code != "disk_required" {
			t.Errorf("vm.snapshot = %+v, want limited with disk_required", entry)
		}
	})

	for _, mode := range []string{"", "mystery"} {
		t.Run("unknown mode "+mode, func(t *testing.T) {
			report := Build(Input{HostChecks: passingChecks(), Target: target("work", mode, "observe")})
			entry := reportEntry(t, report.Capabilities, "vm.snapshot")
			if entry.Status != "unknown" || entry.Reason == nil || entry.Reason.Code != "target_mode_unknown" {
				t.Errorf("vm.snapshot = %+v, want unknown/target_mode_unknown", entry)
			}
		})
	}

	t.Run("partial required host observations are unknown", func(t *testing.T) {
		report := Build(Input{HostChecks: []core.HostCheck{{Name: "qemu-system-x86_64", OK: true}}})
		profile := profileEntry(t, report.Profiles, "qemu-x86_64")
		if profile.Status != "unknown" || profile.Reason == nil || profile.Reason.Code != "host_probe_unavailable" {
			t.Errorf("qemu-x86_64 = %+v, want unknown/host_probe_unavailable", profile)
		}
	})

	t.Run("failed fully observed host requirement is limited", func(t *testing.T) {
		report := Build(Input{HostChecks: []core.HostCheck{
			{Name: "qemu-system-x86_64", OK: true},
			{Name: "qemu-img", OK: false},
			{Name: "/dev/kvm", OK: true},
		}})
		profile := profileEntry(t, report.Profiles, "qemu-x86_64")
		if profile.Status != "limited" || profile.Reason != nil {
			t.Errorf("qemu-x86_64 = %+v, want limited without a reason", profile)
		}
		if len(profile.Limits) != 1 || profile.Limits[0].Code != "host_requirement_missing" || profile.Limits[0].Value != "qemu-img" {
			t.Errorf("qemu-x86_64 limits = %+v, want failed qemu-img", profile.Limits)
		}
	})

	t.Run("empty host observations are unknown", func(t *testing.T) {
		report := Build(Input{HostChecks: []core.HostCheck{}})
		profile := profileEntry(t, report.Profiles, "qemu-x86_64")
		if profile.Status != "unknown" || profile.Reason == nil || profile.Reason.Code != "host_probe_unavailable" {
			t.Errorf("qemu-x86_64 = %+v, want unknown/host_probe_unavailable", profile)
		}
		if report.AccessPolicy.CLIAgentAccessEnforced {
			t.Error("CLIAgentAccessEnforced = true, want false")
		}
	})

	t.Run("metadata loader is adversarial and read-only", func(t *testing.T) {
		t.Setenv("STOAT_HOME", t.TempDir())
		root := os.Getenv("STOAT_HOME")
		vmDir := filepath.Join(root, "requested")
		if err := os.MkdirAll(vmDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(vmDir, "vm.toml"), []byte("name = \"stored\"\nmode = \"cloud\"\nallow_exec = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		fixtures := map[string][]byte{
			filepath.Join(vmDir, "qemu.pid"):     []byte(strconv.Itoa(os.Getpid()) + "\n"),
			filepath.Join(vmDir, "secrets.toml"): []byte("sentinel = \"keep-me\"\n"),
			filepath.Join(root, "stoat.lock"):    []byte("recipe-lock-sentinel\n"),
		}
		before := make(map[string][]byte, len(fixtures))
		for path, body := range fixtures {
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			before[path] = slices.Clone(body)
		}

		loaded, err := LoadTarget("requested")
		if err != nil {
			t.Fatalf("LoadTarget(requested) error = %v", err)
		}
		if loaded.Name != "requested" || loaded.Mode != "cloud" || loaded.AgentAccess != "exec" {
			t.Errorf("LoadTarget(requested) = %+v, want canonical requested/cloud/exec", loaded)
		}
		for path, want := range before {
			got, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !slices.Equal(got, want) {
				t.Errorf("LoadTarget changed %s: got %q, want %q", path, got, want)
			}
		}

		for _, name := range []string{"", "../escape", "bad/name", "-bad", "_bad", "bad name"} {
			if _, err := LoadTarget(name); !errors.Is(err, core.ErrInvalidSpec) {
				t.Errorf("LoadTarget(%q) error = %v, want ErrInvalidSpec", name, err)
			}
		}
		if _, err := LoadTarget("missing"); !errors.Is(err, core.ErrNotFound) {
			t.Errorf("LoadTarget(missing) error = %v, want ErrNotFound", err)
		}
		brokenDir := filepath.Join(root, "broken")
		if err := os.MkdirAll(brokenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(brokenDir, "vm.toml"), []byte("name = \"broken\"\nmode = \"cloud\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadTarget("broken"); !errors.Is(err, core.ErrBroken) {
			t.Errorf("LoadTarget(broken) error = %v, want ErrBroken", err)
		}
	})
}
