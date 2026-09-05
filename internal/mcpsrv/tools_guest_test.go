package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/testutil"
)

func TestReadFileRefusesARelativePath(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	res := callTool(t, "read_file", map[string]any{"vm": "dev", "path": "etc/hosts"})
	if !res.IsError {
		t.Fatal("read_file accepted a relative guest path")
	}
}

func TestReadFileClampsMaxBytes(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, maxReadBytes}, {10, 10}, {1 << 30, maxReadBytes}} {
		if got := readSize(c.in); got != c.want {
			t.Errorf("readSize(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestReadFileArgvIsFixed(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	// stat answers first with a size, then head -c returns the bytes.
	calls := testutil.FakeSSH(t, `case "$1" in stat) echo 5;; *) printf hello;; esac`)
	res := callTool(t, "read_file", map[string]any{"vm": "dev", "path": "/a b;c"})
	if res.IsError {
		t.Fatalf("read_file failed: %+v", res.Content)
	}
	for _, call := range calls.Calls() {
		if !strings.Contains(call.Remote, `'/a b;c'`) {
			t.Fatalf("guest path was not passed as one quoted word: %q", call.Remote)
		}
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Content   string `json:"content"`
		Size      int64  `json:"size"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Content != "hello" || out.Size != 5 || out.Truncated {
		t.Fatalf("got %+v", out)
	}
}

func TestReadFileBase64sBinary(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	testutil.FakeSSH(t, `case "$1" in stat) echo 2;; *) printf '\377\376';; esac`)
	res := callTool(t, "read_file", map[string]any{"vm": "dev", "path": "/bin/x"})
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"encoding":"base64"`) {
		t.Fatalf("binary content was not base64 encoded: %s", raw)
	}
}

func TestListDirCapsEntries(t *testing.T) {
	if got := len(capNames(makeNames(5000))); got != maxDirEntries {
		t.Fatalf("capNames returned %d entries, want %d", got, maxDirEntries)
	}
}

func TestPSCapsRows(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	testutil.FakeSSH(t, `i=0; while [ $i -lt 3000 ]; do echo "$i 1 root 00:01 sleep"; i=$((i+1)); done`)
	res := callTool(t, "ps", map[string]any{"vm": "dev"})
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Processes []any `json:"processes"`
		Truncated bool  `json:"truncated"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Processes) != maxPSRows || !out.Truncated {
		t.Fatalf("got %d rows truncated=%v", len(out.Processes), out.Truncated)
	}
}

func TestGuestReadToolsRefusedAtNone(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "locked", "none")
	for _, name := range []string{"read_file", "list_dir", "stat", "ps", "svc_status", "tail_log"} {
		args := map[string]any{"vm": "locked"}
		switch name {
		case "read_file", "list_dir", "stat":
			args["path"] = "/etc"
		case "svc_status":
			args["name"] = "sshd"
		}
		if res := callTool(t, name, args); !res.IsError {
			t.Errorf("%s ran on a VM at agent_access = none", name)
		}
	}
}

func makeNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "f"
	}
	return out
}
