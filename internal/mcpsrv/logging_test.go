package mcpsrv

import (
	"os"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/logx"
)

// A refused call must reach the log with its tool, its vm and the refusal,
// because an access refusal is the case a person goes to the log to explain.
func TestLogCallsRecordsARefusal(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := logx.Init(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logx.Close() }()
	writeVM(t, "locked", "none")

	if res := callTool(t, "read_file", map[string]any{"vm": "locked", "path": "/etc/hostname"}); !res.IsError {
		t.Fatal("guest tool succeeded below its required access level")
	}

	b, err := os.ReadFile(logx.Path())
	if err != nil {
		t.Fatal(err)
	}
	line := string(b)
	for _, want := range []string{"mcp call refused", "read_file", "locked", "needs observe"} {
		if !strings.Contains(line, want) {
			t.Errorf("log missing %q: %s", want, line)
		}
	}
}

// Arguments never reach the log: a tool that carries a password must log the
// tool name and the vm alone.
func TestLogCallsOmitsArguments(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := logx.Init(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logx.Close() }()
	writeVM(t, "locked", "none")

	callTool(t, "useradd", map[string]any{"vm": "locked", "user": "dev", "password": "hunter2"})

	b, err := os.ReadFile(logx.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Errorf("log leaked an argument value: %s", b)
	}
}
