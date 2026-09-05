package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/testutil"
)

func TestExecRefusedBelowExec(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	res := callTool(t, "exec", map[string]any{"vm": "dev", "argv": []string{"id"}})
	if !res.IsError {
		t.Fatal("exec ran at agent_access = manage")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "needs exec") {
		t.Fatalf("refusal did not name the level: %s", raw)
	}
}

func TestExecClampsTheTimeout(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 60}, {30, 30}, {99999, maxExecSecs}} {
		if got := execTimeout(c.in); int(got.Seconds()) != c.want {
			t.Errorf("execTimeout(%d) = %s, want %ds", c.in, got, c.want)
		}
	}
}

func TestExecEnvNameWithUnderscoreReachesArgv(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	calls := testutil.FakeSSH(t, `true`)
	res := callTool(t, "exec", map[string]any{
		"vm": "dev", "argv": []string{"id"}, "env": map[string]string{"LC_ALL": "C"},
	})
	if res.IsError {
		t.Fatalf("exec failed: %+v", res.Content)
	}
	if !strings.Contains(calls.Calls()[0].Remote, `'LC_ALL=C'`) {
		t.Fatalf("env var did not reach argv: %q", calls.Calls()[0].Remote)
	}
}

func TestWriteFileRefusesOnAStoppedVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	testutil.FakeSSH(t, `exit 1`)
	res := callTool(t, "write_file", map[string]any{"vm": "dev", "path": "/tmp/x", "content": "x"})
	if !res.IsError {
		t.Fatal("write_file ran on a stopped VM")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not running") {
		t.Fatalf("refusal did not report not_running: %s", raw)
	}
}

func TestExecRefusesOnAStoppedVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	testutil.FakeSSH(t, `exit 1`)
	res := callTool(t, "exec", map[string]any{"vm": "dev", "argv": []string{"id"}})
	if !res.IsError {
		t.Fatal("exec ran on a stopped VM")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not running") {
		t.Fatalf("refusal did not report not_running: %s", raw)
	}
}

func TestExecBgRefusesOnAStoppedVM(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	testutil.FakeSSH(t, `exit 1`)
	res := callTool(t, "exec_bg", map[string]any{"vm": "dev", "argv": []string{"true"}})
	if !res.IsError {
		t.Fatal("exec_bg ran on a stopped VM")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "not running") {
		t.Fatalf("refusal did not report not_running: %s", raw)
	}
}

func TestExecBgThenStatusThenOutputThenKill(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	// The fake answers every guest call: mkdir and nohup for exec_bg, cat
	// for the exit file, head for the output, kill for the signal.
	testutil.FakeSSH(t, `
case "$1" in
  mkdir|nohup|sh) exit 0;;
  cat) echo 0;;
  stat) echo 5;;
  head) printf hello;;
  kill) exit 0;;
  *) exit 0;;
esac`)

	res := callTool(t, "exec_bg", map[string]any{"vm": "dev", "argv": []string{"sleep", "60"}})
	if res.IsError {
		t.Fatalf("exec_bg failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var started struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(started.JobID, "j-") {
		t.Fatalf("job_id = %q", started.JobID)
	}

	if res := callTool(t, "list_jobs", map[string]any{"vm": "dev"}); res.IsError {
		t.Fatalf("list_jobs failed: %+v", res.Content)
	}

	res = callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": started.JobID})
	raw, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"state":"exited"`) {
		t.Fatalf("job_status = %s, want exited", raw)
	}

	res = callTool(t, "job_output", map[string]any{"vm": "dev", "job_id": started.JobID})
	raw, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), "hello") {
		t.Fatalf("job_output = %s", raw)
	}

	if res := callTool(t, "job_kill", map[string]any{"vm": "dev", "job_id": started.JobID}); res.IsError {
		t.Fatalf("job_kill failed: %+v", res.Content)
	}
}

func TestJobStatusIsUnknownAfterAReboot(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	// A reboot clears /run, so the exit file and the pid are both gone.
	testutil.FakeSSH(t, `exit 1`)
	if err := saveJob("dev", job{ID: "j-00000001", Argv: []string{"true"}, User: "stoat", Dir: "/run/stoat/jobs/j-00000001"}); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": "j-00000001"})
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"state":"unknown"`) {
		t.Fatalf("job_status = %s, want unknown", raw)
	}
}

func TestJobIDIsValidated(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	for _, id := range []string{"", "../../etc", "j-XYZ", "j-9f3c1e2a1"} {
		if res := callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": id}); !res.IsError {
			t.Errorf("job_status accepted job_id %q", id)
		}
	}
}
