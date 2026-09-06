package mcpsrv

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCapabilitiesMCPToolIsOptionalVMReadOnly(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	var capabilityToolName = "capabilities"
	var toolFound bool
	var toolInputSchema json.RawMessage
	var annotationsReadOnly, annotationsDestructive, annotationsOpenWorld bool
	for _, tool := range listTools(t) {
		if tool.Name != capabilityToolName {
			continue
		}
		toolFound = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Error("capabilities is not marked read-only")
		} else {
			annotationsReadOnly = tool.Annotations.ReadOnlyHint
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
			t.Error("capabilities is marked destructive")
		} else {
			annotationsDestructive = *tool.Annotations.DestructiveHint
		}
		if tool.Annotations == nil || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Error("capabilities is marked open-world")
		} else {
			annotationsOpenWorld = *tool.Annotations.OpenWorldHint
		}
		var err error
		toolInputSchema, err = json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal capabilities input schema: %v", err)
		}
	}
	if !toolFound {
		t.Fatal("capabilities tool is not registered")
	}
	if !annotationsReadOnly || annotationsDestructive || annotationsOpenWorld {
		t.Fatalf("capabilities annotations = read-only %v, destructive %v, open-world %v", annotationsReadOnly, annotationsDestructive, annotationsOpenWorld)
	}
	var schema struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(toolInputSchema, &schema); err != nil {
		t.Fatalf("decode capabilities input schema: %v; schema=%s", err, toolInputSchema)
	}
	if _, ok := schema.Properties["vm"]; !ok {
		t.Fatalf("capabilities input schema has no optional vm property: %s", toolInputSchema)
	}
	if slices.Contains(schema.Required, "vm") {
		t.Errorf("capabilities input schema requires optional vm: %s", toolInputSchema)
	}
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Errorf("capabilities input schema additionalProperties = %v, want false", schema.AdditionalProperties)
	}

	res := callTool(t, capabilityToolName, map[string]any{})
	if res.IsError {
		t.Fatalf("capabilities without vm failed: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("capabilities output is not a report: %v; output=%s", err, raw)
	}
	if out.Schema != 1 {
		t.Errorf("capabilities schema = %d, want 1; output=%s", out.Schema, raw)
	}
}
