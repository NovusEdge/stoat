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

// TestTailLogEscalatesOnlyForTheGuestsOwnLog pins where tail_log's root
// applies. The caller's own path runs as the ssh user, or an agent at
// observe would read any file as root through tail_log and get more than
// read_file grants at the same level. The guest file's own log path carries
// no tool input and still runs escalated.
func TestTailLogEscalatesOnlyForTheGuestsOwnLog(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	// sshx.Escalate is a no-op for a root ssh user, and writeVM leaves the
	// user empty, which reads as root.
	setSSHUser(t, "dev", "stoat")

	calls := testutil.FakeSSH(t, `echo line`)
	if res := callTool(t, "tail_log", map[string]any{"vm": "dev", "path": "/etc/shadow"}); res.IsError {
		t.Fatalf("tail_log failed: %+v", res.Content)
	}
	got := calls.Calls()
	if len(got) != 1 {
		t.Fatalf("got %d ssh calls, want 1", len(got))
	}
	if strings.Contains(got[0].Remote, "sudo") {
		t.Fatalf("tail_log escalated for a caller-supplied path: %q", got[0].Remote)
	}

	calls = testutil.FakeSSH(t, `echo line`)
	if res := callTool(t, "tail_log", map[string]any{"vm": "dev"}); res.IsError {
		t.Fatalf("tail_log failed: %+v", res.Content)
	}
	got = calls.Calls()
	if len(got) != 1 {
		t.Fatalf("got %d ssh calls, want 1", len(got))
	}
	if !strings.Contains(got[0].Remote, "sudo") {
		t.Fatalf("tail_log did not escalate for the guest's own log: %q", got[0].Remote)
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

func TestWriteFileSetsTheMode(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	calls := testutil.FakeSSH(t, `cat > /dev/null; true`)
	res := callTool(t, "write_file", map[string]any{
		"vm": "dev", "path": "/etc/app.conf", "content": "k=v\n", "mode": "0600",
	})
	if res.IsError {
		t.Fatalf("write_file failed: %+v", res.Content)
	}
	got := calls.Calls()
	if len(got) != 2 {
		t.Fatalf("want a write then a chmod, got %d calls", len(got))
	}
	if !strings.Contains(got[0].Remote, "'tee' '/etc/app.conf'") {
		t.Fatalf("write argv = %q", got[0].Remote)
	}
	if !strings.Contains(got[1].Remote, "'chmod' '0600' '/etc/app.conf'") {
		t.Fatalf("chmod argv = %q", got[1].Remote)
	}
}

func TestWriteFileAppends(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	calls := testutil.FakeSSH(t, `cat > /dev/null; true`)
	callTool(t, "write_file", map[string]any{
		"vm": "dev", "path": "/etc/app.conf", "content": "x", "append": true,
	})
	if !strings.Contains(calls.Calls()[0].Remote, "'tee' '-a'") {
		t.Fatalf("append did not use tee -a: %q", calls.Calls()[0].Remote)
	}
}

func TestWriteFileRefusesAtObserve(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	res := callTool(t, "write_file", map[string]any{"vm": "dev", "path": "/tmp/x", "content": "x"})
	if !res.IsError {
		t.Fatal("write_file ran at agent_access = observe")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "needs manage") {
		t.Fatalf("refusal did not name the level: %s", raw)
	}
}

func TestSvcRefusesAnUnknownAction(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	if res := callTool(t, "svc", map[string]any{"vm": "dev", "name": "sshd", "action": "reload"}); !res.IsError {
		t.Fatal("svc accepted an action outside the four verbs")
	}
}

func TestSvcPassesTheNameAsAPositional(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	calls := testutil.FakeSSH(t, `true`)
	callTool(t, "svc", map[string]any{"vm": "dev", "name": "sshd", "action": "restart"})
	remote := calls.Calls()[0].Remote
	// The template is the shell body and the name is $1, so a name that
	// looks like shell syntax cannot become syntax.
	if !strings.Contains(remote, `'stoat_svc' 'sshd'`) {
		t.Fatalf("svc argv = %q", remote)
	}
}

func TestPkgInstallRunsSetupThenInstall(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	calls := testutil.FakeSSH(t, `true`)
	res := callTool(t, "pkg_install", map[string]any{"vm": "dev", "packages": []string{"curl", "jq"}})
	if res.IsError {
		t.Fatalf("pkg_install failed: %+v", res.Content)
	}
	got := calls.Calls()
	if len(got) != 2 {
		t.Fatalf("want setup then install, got %d calls", len(got))
	}
	if !strings.Contains(got[1].Remote, "'curl' 'jq'") {
		t.Fatalf("install argv = %q", got[1].Remote)
	}
}

func TestPkgInstallRefusesAFlagPackage(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	if res := callTool(t, "pkg_install", map[string]any{"vm": "dev", "packages": []string{"--force"}}); !res.IsError {
		t.Fatal("pkg_install accepted a package name that reads as a flag")
	}
}

func TestCopyToConfinesTheHostPath(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, "dev", "manage")
	res := callTool(t, "copy_to", map[string]any{"vm": "dev", "local": "/etc/passwd", "remote": "/tmp/x"})
	if !res.IsError {
		t.Fatal("copy_to accepted a host path outside the VM's shared directory")
	}
}
