package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/mcpsrv"
)

// parseOnly runs Parse and reports whether argv parses, without running any
// command.
func parseOnly(t *testing.T, argv []string) error {
	t.Helper()
	_, err := Parse(argv)
	return err
}

// runCLI runs Main and returns stdout.
func runCLI(t *testing.T, argv ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	Main(argv, "test-version", strings.NewReader(""), &out, &errOut)
	return out.String()
}

func TestHTTPRefusesANonLoopbackAddress(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", "192.168.1.5:7777", ":7777", "example.com:7777"} {
		if err := mcpsrv.CheckLoopback(addr); err == nil {
			t.Errorf("CheckLoopback(%q) accepted a non-loopback bind", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:7777", "localhost:7777", "[::1]:7777"} {
		if err := mcpsrv.CheckLoopback(addr); err != nil {
			t.Errorf("CheckLoopback(%q): %v", addr, err)
		}
	}
}

func TestMCPIsInTheGrammar(t *testing.T) {
	// The mcp command must parse with no subcommand and default to serve,
	// because every MCP client launches "stoat mcp" as a subprocess.
	for _, argv := range [][]string{
		{"mcp"},
		{"mcp", "--http", "127.0.0.1:7777"},
		{"mcp", "install", "claude-code"},
		{"mcp", "install", "vscode", "--print"},
		{"mcp", "doctor"},
	} {
		if err := parseOnly(t, argv); err != nil {
			t.Errorf("%v: %v", argv, err)
		}
	}
	if err := parseOnly(t, []string{"mcp", "install", "emacs"}); err == nil {
		t.Error("an unknown client was accepted")
	}
}

func TestMCPDoctorJSON(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	out := runCLI(t, "--json", "mcp", "doctor")
	for _, key := range []string{`"contract"`, `"transport"`, `"binary"`, `"clients"`} {
		if !strings.Contains(out, key) {
			t.Errorf("mcp doctor output has no %s: %s", key, out)
		}
	}
}
