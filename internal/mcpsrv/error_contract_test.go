package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/core"
)

const testErrorMetaKey = "io.github.novusedge.stoat/error"

type errorContractIn struct{}

type errorContractOut struct {
	Items []string `json:"items"`
}

type errorContractInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func errorContractClient(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := New(opts)
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
	return cs
}

func addErrorContractTool(t *testing.T, srv *mcp.Server, errFn func() error) {
	t.Helper()
	register(srv, "error_contract_test", classRead, "test-only typed tool for the MCP error contract", func(context.Context, errorContractIn) (errorContractOut, error) {
		return errorContractOut{}, errFn()
	})
}

func decodeErrorContract(t *testing.T, res *mcp.CallToolResult) (errorContractInfo, errorContractInfo) {
	t.Helper()
	if len(res.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2: %+v", len(res.Content), res.Content)
	}
	first, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content block 1 = %T, want text", res.Content[0])
	}
	second, ok := res.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content block 2 = %T, want text", res.Content[1])
	}
	var metadata errorContractInfo
	raw, err := json.Marshal(res.Meta[testErrorMetaKey])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("metadata is not an error object: %s: %v", raw, err)
	}
	var fallback struct {
		Error errorContractInfo `json:"error"`
	}
	if err := json.Unmarshal([]byte(second.Text), &fallback); err != nil {
		t.Fatalf("fallback is not valid JSON: %q: %v", second.Text, err)
	}
	if metadata != fallback.Error {
		t.Fatalf("metadata and fallback differ: %+v != %+v", metadata, fallback.Error)
	}
	return metadata, errorContractInfo{Message: first.Text}
}

func TestMCPErrorContractUnknownFallback(t *testing.T) {
	srv := New(Options{Version: "test", Limits: DefaultLimits()})
	addErrorContractTool(t, srv, func() error { return errors.New("unexpected backend failure") })
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "error_contract_test", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("unknown handler error did not set isError")
	}
	metadata, first := decodeErrorContract(t, res)
	if metadata.Code != "internal" || metadata.Message != "unexpected backend failure" || first.Message != metadata.Message {
		t.Fatalf("error = %+v, first = %+v", metadata, first)
	}
	var out errorContractOut
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("structured content is not typed output: %s: %v", raw, err)
	}
	if out.Items == nil || len(out.Items) != 0 {
		t.Fatalf("structured content = %+v, want typed empty output", out)
	}
}

func TestMCPErrorContractKnownSentinel(t *testing.T) {
	ctx := context.Background()
	srv := New(Options{Version: "test", Limits: DefaultLimits()})
	addErrorContractTool(t, srv, func() error { return fmt.Errorf("%w: dev", core.ErrNotFound) })
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "error_contract_test", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, first := decodeErrorContract(t, res)
	if metadata.Code != "not_found" || metadata.Message != "not found: dev" || first.Message != metadata.Message {
		t.Fatalf("error = %+v, first = %+v", metadata, first)
	}
}

func TestMCPErrorContractAccessAndRateRefusals(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "locked", "none")
	access := callTool(t, "read_file", map[string]any{"vm": "locked", "path": "/etc/hostname"})
	if !access.IsError {
		t.Fatal("guest tool succeeded below its required access level")
	}
	accessMeta, accessFirst := decodeErrorContract(t, access)
	if accessMeta.Code != "access_denied" || accessMeta.Message != accessFirst.Message || accessFirst.Message != `vm "locked" has agent_access = none; needs observe` {
		t.Fatalf("access refusal = %+v, first = %+v", accessMeta, accessFirst)
	}

	cs := errorContractClient(t, Options{Version: "test", Limits: Limits{ToolBurst: 1, ToolRate: 0.001, Burst: 100, Rate: 2}})
	ctx := context.Background()
	for i := range 2 {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_vms", Arguments: map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if res.IsError {
				t.Fatalf("first limited call failed: %+v", res.Content)
			}
			continue
		}
		if !res.IsError {
			t.Fatal("second limited call was not refused")
		}
		meta, first := decodeErrorContract(t, res)
		const ratePrefix = "rate limit reached for list_vms; retry in about "
		if meta.Code != "rate_limited" || first.Message != meta.Message || !strings.HasPrefix(first.Message, ratePrefix) || strings.TrimPrefix(first.Message, ratePrefix) == "" {
			t.Fatalf("rate refusal = %+v, first = %+v", meta, first)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "doctor", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("unrelated tool was charged by refusal: %v %+v", err, res)
	}
}

func TestMCPErrorContractSuccessAndSchemaUnchanged(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	cs := errorContractClient(t, Options{Version: "test", Limits: DefaultLimits()})
	ctx := context.Background()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var list *mcp.Tool
	for _, tool := range tools.Tools {
		if tool.Name == "list_vms" {
			list = tool
			break
		}
	}
	if list == nil || list.OutputSchema == nil {
		t.Fatal("list_vms output schema is missing")
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_vms", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list_vms failed: %+v", res.Content)
	}
	if _, ok := res.Meta[testErrorMetaKey]; ok || len(res.Content) != 1 {
		t.Fatalf("success result has error representation: meta=%v content=%d", res.Meta, len(res.Content))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		VMs []any `json:"vms"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.VMs == nil {
		t.Fatalf("list_vms structured output = %s: %v", raw, err)
	}
}

func TestMCPErrorContractEscapedMessages(t *testing.T) {
	message := "unexpected \\\"backend\\\"\nfailed: {\"reason\": [1, 2]}"
	ctx := context.Background()
	srv := New(Options{Version: "test", Limits: DefaultLimits()})
	addErrorContractTool(t, srv, func() error { return errors.New(message) })
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "error_contract_test", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	metadata, first := decodeErrorContract(t, res)
	if metadata.Code != "internal" || metadata.Message != message || first.Message != message {
		t.Fatalf("escaped message changed: metadata=%+v first=%+v", metadata, first)
	}
	if !reflect.DeepEqual(metadata.Message, message) {
		t.Fatal("decoded fallback message is not byte-for-byte equal")
	}

	const secret = "sentinel-secret"
	srv = New(Options{Version: "test", Limits: DefaultLimits()})
	register(srv, "error_contract_json_message_test", classRead, "test-only typed tool for JSON error message redaction", func(context.Context, errorContractIn) (errorContractOut, error) {
		return errorContractOut{}, errors.New(`{"password":"` + secret + `","note":"keep"}`)
	})
	ct, st = mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err = mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "error_contract_json_message_test", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" {
		t.Fatal("serialized result is empty")
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("redacted JSON error leaked secret: %s", raw)
	}
	metadata, first = decodeErrorContract(t, res)
	if metadata.Message != first.Message {
		t.Fatalf("metadata message %q differs from redacted human message %q", metadata.Message, first.Message)
	}
}
