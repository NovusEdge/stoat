//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/mcpsrv"
)

func TestCapabilitiesCLIUsesHumanAndJSONForms(t *testing.T) {
	cliRoot(t)

	var humanOut, humanErr bytes.Buffer
	if code := Main([]string{"capabilities"}, "v-test", strings.NewReader(""), &humanOut, &humanErr); code != ExitOK {
		t.Fatalf("human capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, humanOut.String(), humanErr.String())
	}
	if strings.Contains(humanOut.String(), "{") {
		t.Fatalf("human capabilities emitted a JSON object: %q", humanOut.String())
	}
	for _, header := range []string{"NAME", "STATUS", "SCOPE"} {
		if !strings.Contains(strings.ToUpper(humanOut.String()), header) {
			t.Errorf("human capabilities omitted %s header: %q", header, humanOut.String())
		}
	}

	var jsonOut, jsonErr bytes.Buffer
	if code := Main([]string{"--json", "capabilities"}, "v-test", strings.NewReader(""), &jsonOut, &jsonErr); code != ExitOK {
		t.Fatalf("JSON capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, jsonOut.String(), jsonErr.String())
	}
	var envelope struct {
		Cmd  string `json:"cmd"`
		OK   bool   `json:"ok"`
		Data struct {
			Schema int `json:"schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &envelope); err != nil {
		t.Fatalf("JSON capabilities output is not one envelope: %v; output=%q", err, jsonOut.String())
	}
	if envelope.Cmd != "capabilities" || !envelope.OK || envelope.Data.Schema != 1 {
		t.Errorf("JSON capabilities envelope = %+v, want cmd=capabilities ok=true data.schema=1", envelope)
	}
}

func callCapabilitiesMCP(t *testing.T, version string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx := context.Background()
	srv := mcpsrv.New(mcpsrv.Options{Version: version, Limits: mcpsrv.DefaultLimits()})
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
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "capabilities", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decodeCapabilitiesEnvelope(t *testing.T, output []byte) capabilities.Report {
	t.Helper()
	var envelope struct {
		Cmd  string          `json:"cmd"`
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &envelope); err != nil {
		t.Fatalf("capabilities output is not one JSON envelope: %v; output=%q", err, output)
	}
	if envelope.Cmd != "capabilities" || !envelope.OK {
		t.Fatalf("capabilities envelope = %+v, want cmd=capabilities and ok=true", envelope)
	}
	var report capabilities.Report
	if err := json.Unmarshal(envelope.Data, &report); err != nil {
		t.Fatalf("capabilities data is not a report: %v; data=%s", err, envelope.Data)
	}
	return report
}

func TestCapabilitiesCLIAndMCPReportsHaveSemanticParity(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "myrepo-dev", Mode: "cloud", AgentAccess: "observe"}).Save(); err != nil {
		t.Fatal(err)
	}

	var cliOut, cliErr bytes.Buffer
	if code := Main([]string{"--json", "capabilities", "myrepo-dev"}, "v-test", strings.NewReader(""), &cliOut, &cliErr); code != ExitOK {
		t.Fatalf("CLI capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, cliOut.String(), cliErr.String())
	}
	cliReport := decodeCapabilitiesEnvelope(t, cliOut.Bytes())
	res := callCapabilitiesMCP(t, "v-test", map[string]any{"vm": "myrepo-dev"})
	if res.IsError {
		t.Fatalf("MCP capabilities failed: %+v", res.Content)
	}
	mcpRaw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var mcpReport capabilities.Report
	if err := json.Unmarshal(mcpRaw, &mcpReport); err != nil {
		t.Fatalf("MCP capabilities data is not a report: %v; output=%s", err, mcpRaw)
	}
	if !reflect.DeepEqual(cliReport, mcpReport) {
		t.Fatalf("CLI and MCP capability reports differ:\nCLI: %+v\nMCP: %+v", cliReport, mcpReport)
	}
	if cliReport.Target == nil || cliReport.Target.Name != "myrepo-dev" || cliReport.Host.ProjectState != "available" || cliReport.StoatVersion != "v-test" {
		t.Errorf("shared report target/version/project = target=%+v version=%q project=%q", cliReport.Target, cliReport.StoatVersion, cliReport.Host.ProjectState)
	}
}

func TestCapabilitiesCLIMissingTargetUsesNotFound(t *testing.T) {
	cliRoot(t)
	var out, errOut bytes.Buffer
	if code := Main([]string{"--json", "capabilities", "missing"}, "v-test", strings.NewReader(""), &out, &errOut); code != ExitFail {
		t.Fatalf("missing target exit = %d, want ExitFail; stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &envelope); err != nil {
		t.Fatalf("missing target output is not one JSON envelope: %v; output=%q", err, out.String())
	}
	if envelope.OK || envelope.Error.Code != "not_found" {
		t.Errorf("missing target envelope = %+v, want ok=false error.code=not_found", envelope)
	}
}

func TestCapabilitiesGlobalVMInProjectPreservesStalePID(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "outside", Mode: "cloud"}).Save(); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(config.Root(), "outside", "qemu.pid")
	want := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if err := os.WriteFile(pidPath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := Main([]string{"capabilities", "outside"}, "v-test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("capabilities global VM exit = %d, want ExitOK; stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	got, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("capabilities changed stale PID: got %q, want %q", got, want)
	}
}

func TestCapabilitiesTargetlessDoesNotInitializeStoatHome(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join(t.TempDir(), "stoat-home")
	t.Setenv("STOAT_HOME", root)

	var humanOut, humanErr bytes.Buffer
	if code := Main([]string{"capabilities"}, "v-test", strings.NewReader(""), &humanOut, &humanErr); code != ExitOK {
		t.Fatalf("targetless human capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, humanOut.String(), humanErr.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("targetless human capabilities initialized STOAT_HOME: stat error = %v", err)
	}

	var jsonOut, jsonErr bytes.Buffer
	if code := Main([]string{"--json", "capabilities"}, "v-test", strings.NewReader(""), &jsonOut, &jsonErr); code != ExitOK {
		t.Fatalf("targetless JSON capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, jsonOut.String(), jsonErr.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("targetless JSON capabilities initialized STOAT_HOME: stat error = %v", err)
	}
}
