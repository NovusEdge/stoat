package mcpsrv

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listTools drives the server with the SDK's in-process client, so the test
// sees exactly what a real client sees.
func listTools(t *testing.T) []*mcp.Tool {
	t.Helper()
	ctx := context.Background()
	srv := New(Options{Version: "test", Limits: DefaultLimits()})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Error(err)
		}
	})
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Tools
}

func TestEveryTableToolIsRegistered(t *testing.T) {
	got := map[string]bool{}
	for _, tool := range listTools(t) {
		got[tool.Name] = true
	}
	for _, spec := range toolTable {
		if !got[spec.Name] {
			if owner, ok := pending[spec.Name]; ok {
				t.Logf("tool %q is pending, owned by %s", spec.Name, owner)
				continue
			}
			t.Errorf("tool %q is in the table but not registered", spec.Name)
		}
	}
}

func TestNoToolOutsideTheTable(t *testing.T) {
	want := map[string]bool{}
	for _, spec := range toolTable {
		want[spec.Name] = true
	}
	for _, tool := range listTools(t) {
		if !want[tool.Name] {
			t.Errorf("tool %q is registered but not in the table", tool.Name)
		}
	}
}

func TestForbiddenSurfacesAbsent(t *testing.T) {
	for _, tool := range listTools(t) {
		if slices.Contains(forbiddenSurfaces, tool.Name) {
			t.Errorf("forbidden surface %q is registered", tool.Name)
		}
	}
}

func TestInputSchemaRejectsAdditionalProperties(t *testing.T) {
	for _, tool := range listTools(t) {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		var schema struct {
			AdditionalProperties *bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
			t.Errorf("%s: additionalProperties is not false: %s", tool.Name, raw)
		}
	}
}

func TestAnnotationsMatchTable(t *testing.T) {
	byName := map[string]*mcp.Tool{}
	for _, tool := range listTools(t) {
		byName[tool.Name] = tool
	}
	for _, spec := range toolTable {
		tool, ok := byName[spec.Name]
		if !ok {
			continue // TestEveryTableToolIsRegistered reports this.
		}
		want := annotationsFor(spec.Class)
		got := tool.Annotations
		if got == nil {
			t.Errorf("%s: no annotations", spec.Name)
			continue
		}
		if got.ReadOnlyHint != want.ReadOnlyHint {
			t.Errorf("%s: readOnlyHint = %v, want %v", spec.Name, got.ReadOnlyHint, want.ReadOnlyHint)
		}
		if got.DestructiveHint == nil || *got.DestructiveHint != *want.DestructiveHint {
			t.Errorf("%s: destructiveHint = %v, want %v", spec.Name, got.DestructiveHint, *want.DestructiveHint)
		}
		if got.OpenWorldHint == nil || *got.OpenWorldHint != *want.OpenWorldHint {
			t.Errorf("%s: openWorldHint = %v, want %v", spec.Name, got.OpenWorldHint, *want.OpenWorldHint)
		}
	}
}

func TestNoForbiddenInputField(t *testing.T) {
	for _, tool := range listTools(t) {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range forbiddenInputFields {
			if strings.Contains(string(raw), `"`+field+`"`) {
				t.Errorf("%s: input schema mentions forbidden field %q", tool.Name, field)
			}
		}
	}
}

func TestEveryToolHasDescription(t *testing.T) {
	for _, tool := range listTools(t) {
		if len(strings.TrimSpace(tool.Description)) < 40 {
			t.Errorf("%s: description is too short to be honest: %q", tool.Name, tool.Description)
		}
	}
}

func TestNoEmDashInDescription(t *testing.T) {
	for _, tool := range listTools(t) {
		if strings.ContainsRune(tool.Description, '—') {
			t.Errorf("%s: description contains an em dash", tool.Name)
		}
	}
}

// callTool drives one tool through the in-process client and returns the
// result. It is the round trip an MCP client makes.
func callTool(t *testing.T, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	srv := New(Options{Version: "test", Limits: DefaultLimits()})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Error(err)
		}
	})
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func TestListVMsRoundTrip(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "list_vms", map[string]any{})
	if res.IsError {
		t.Fatalf("list_vms failed: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		VMs []any `json:"vms"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("list_vms output is not wire.VMList: %s", raw)
	}
	if out.VMs == nil {
		t.Fatalf("vms is null, want an empty list: %s", raw)
	}
}

func TestLogsClampsLines(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 1}, {50, 50}, {5000, maxLogLines}} {
		if got := clampInt(c.in, 1, maxLogLines); got != c.want {
			t.Errorf("clampInt(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
