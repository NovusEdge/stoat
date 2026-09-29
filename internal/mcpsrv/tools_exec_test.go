package mcpsrv

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestExecTimeoutReturnsPartialOutput(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	// exec, so the kill lands on sleep itself: a shell parent would leave it
	// holding the output pipe open.
	calls := testutil.FakeSSH(t, `printf partial; exec sleep 30`)
	res := callTool(t, "exec", map[string]any{"vm": "dev", "argv": []string{"slow"}, "timeout_seconds": 1})
	if res.IsError {
		t.Fatalf("a timeout came back as an error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var got struct {
		Stdout   string `json:"stdout"`
		TimedOut bool   `json:"timed_out"`
		Message  string `json:"message"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Stdout != "partial" || !got.TimedOut || !strings.Contains(got.Message, "exec_bg") {
		t.Fatalf("result = %s", raw)
	}
	if !strings.Contains(calls.Calls()[0].Remote, "timeout -s KILL") {
		t.Fatalf("the guest was not given its own time limit: %q", calls.Calls()[0].Remote)
	}
}

func TestExecOnABootingVMSaysToWait(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	testutil.FakeSSH(t, `echo "Connection timed out during banner exchange" >&2; exit 255`)
	res := callTool(t, "exec", map[string]any{"vm": "dev", "argv": []string{"id"}})
	if !res.IsError {
		t.Fatal("exec reported success when ssh never connected")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "call wait first") {
		t.Fatalf("error did not point at wait: %s", raw)
	}
	meta, _ := json.Marshal(res.Meta)
	if !strings.Contains(string(meta), "cannot_reach") {
		t.Fatalf("code is not cannot_reach: %s", meta)
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

// /run/stoat is root-owned on a real guest, so the job directory must be made
// escalated and chowned to the ssh user, and the runner must survive the ssh
// session ending. A live Debian 13 boot found both gaps.
func TestExecBgMakesTheJobDirEscalatedAndRunsUnderNohup(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	setSSHUser(t, "dev", "stoat")
	calls := testutil.FakeSSH(t, `exit 0`)

	if res := callTool(t, "exec_bg", map[string]any{"vm": "dev", "argv": []string{"sleep", "60"}}); res.IsError {
		t.Fatalf("exec_bg failed: %+v", res.Content)
	}
	got := calls.Calls()
	if len(got) != 2 {
		t.Fatalf("got %d ssh calls, want mkdir then start", len(got))
	}
	mk := got[0].Remote
	if !strings.Contains(mk, "sudo") || !strings.Contains(mk, "chown") || !strings.Contains(mk, "'stoat'") {
		t.Fatalf("job dir was not made escalated and chowned to the ssh user: %q", mk)
	}
	start := got[1].Remote
	if strings.Contains(start, "sudo") {
		t.Fatalf("the job itself ran escalated: %q", start)
	}
	if !strings.Contains(start, "nohup") {
		t.Fatalf("the runner does not survive the ssh session: %q", start)
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

func TestJobIDErrorCodes(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	for id, want := range map[string]string{"j-XYZ": "usage", "j-00000009": "not_found"} {
		res := callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": id})
		if !res.IsError {
			t.Fatalf("job_status accepted %q", id)
		}
		if meta, _ := decodeErrorContract(t, res); meta.Code != want {
			t.Errorf("job_id %q answered %q, want %q", id, meta.Code, want)
		}
	}
}

// sameJSON compares a structured result with the JSON it should carry,
// ignoring key order, which the SDK does not fix.
func sameJSON(t *testing.T, got any, want string) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
}

// jobFake answers the guest calls jobState makes: the exit file, the pid file
// and kill -0. state is "running" or "exited".
func jobFake(t *testing.T, state string) *testutil.SSHCalls {
	t.Helper()
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	if err := saveJob("dev", job{ID: "j-00000001", Argv: []string{"true"}, User: "stoat", Dir: "/run/stoat/jobs/j-00000001"}); err != nil {
		t.Fatal(err)
	}
	return testutil.FakeSSH(t, `case "$1" in
  cat) case "$2" in
    */exit) [ "`+state+`" = exited ] && echo 3 || exit 1;;
    */pid) echo 42;;
  esac;;
  tail) case "$4" in */out) printf 'the out';; */err) printf 'the err';; esac;;
  *) exit 0;;
esac`)
}

func TestJobStatusOmitsExitCodeWhileRunning(t *testing.T) {
	jobFake(t, "running")
	res := callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": "j-00000001"})
	sameJSON(t, res.StructuredContent, `{"job_id":"j-00000001","state":"running"}`)
}

func TestJobStatusReportsAnExitCode(t *testing.T) {
	jobFake(t, "exited")
	res := callTool(t, "job_status", map[string]any{"vm": "dev", "job_id": "j-00000001"})
	sameJSON(t, res.StructuredContent, `{"job_id":"j-00000001","state":"exited","exit_code":3}`)
}

func TestJobKillOnAnExitedJobReportsItsState(t *testing.T) {
	calls := jobFake(t, "exited")
	res := callTool(t, "job_kill", map[string]any{"vm": "dev", "job_id": "j-00000001"})
	if res.IsError {
		t.Fatalf("job_kill failed: %+v", res.Content)
	}
	sameJSON(t, res.StructuredContent, `{"job_id":"j-00000001","state":"exited","exit_code":3,"signaled":false}`)
	for _, c := range calls.Calls() {
		if strings.Contains(c.Remote, "'kill'") {
			t.Fatalf("an exited job was signaled: %q", c.Remote)
		}
	}
}

func TestJobKillSignalsARunningJob(t *testing.T) {
	calls := jobFake(t, "running")
	res := callTool(t, "job_kill", map[string]any{"vm": "dev", "job_id": "j-00000001", "signal": "KILL"})
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"signaled":true`) {
		t.Fatalf("job_kill = %s", raw)
	}
	var sent bool
	for _, c := range calls.Calls() {
		sent = sent || strings.Contains(c.Remote, "'kill' '-KILL' '42'")
	}
	if !sent {
		t.Fatalf("no kill -KILL 42 in %+v", calls.Calls())
	}
}

func TestJobWaitReturnsTheExitCodeAndTails(t *testing.T) {
	jobFake(t, "exited")
	res := callTool(t, "job_wait", map[string]any{"vm": "dev", "job_id": "j-00000001"})
	if res.IsError {
		t.Fatalf("job_wait failed: %+v", res.Content)
	}
	sameJSON(t, res.StructuredContent, `{"job_id":"j-00000001","state":"exited","exit_code":3,"stdout":"the out","stderr":"the err"}`)
}

func TestJobWaitTimesOutOnARunningJob(t *testing.T) {
	old := jobPollInterval
	jobPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { jobPollInterval = old })
	jobFake(t, "running")
	res := callTool(t, "job_wait", map[string]any{"vm": "dev", "job_id": "j-00000001", "timeout_seconds": 1})
	if res.IsError {
		t.Fatalf("a job outliving the wait is not an error: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(raw), `"state":"running"`) || !strings.Contains(string(raw), `"timed_out":true`) {
		t.Fatalf("job_wait = %s", raw)
	}
}

func TestListJobsCarriesState(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	for _, id := range []string{"j-00000001", "j-00000002"} {
		if err := saveJob("dev", job{ID: id, Argv: []string{"true"}, User: "stoat", Dir: "/run/stoat/jobs/" + id}); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first job's directory survived a reboot.
	testutil.FakeSSH(t, `shift 4; for d; do case "$d" in *1) printf '%s\texited\t3\n' "$d";; esac; done`)
	res := callTool(t, "list_jobs", map[string]any{"vm": "dev"})
	if res.IsError {
		t.Fatalf("list_jobs failed: %+v", res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Jobs []struct {
			ID       string `json:"job_id"`
			State    string `json:"state"`
			ExitCode *int   `json:"exit_code"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Jobs) != 2 ||
		out.Jobs[0].State != "exited" || out.Jobs[0].ExitCode == nil || *out.Jobs[0].ExitCode != 3 ||
		out.Jobs[1].State != "unknown" || out.Jobs[1].ExitCode != nil {
		t.Fatalf("list_jobs = %s", raw)
	}
}

func TestListJobsOnAStoppedVMIsUnknown(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "exec")
	if err := saveJob("dev", job{ID: "j-00000001", Argv: []string{"true"}, User: "stoat", Dir: "/run/stoat/jobs/j-00000001"}); err != nil {
		t.Fatal(err)
	}
	testutil.FakeSSH(t, `exit 255`)
	res := callTool(t, "list_jobs", map[string]any{"vm": "dev"})
	raw, _ := json.Marshal(res.StructuredContent)
	if res.IsError || !strings.Contains(string(raw), `"state":"unknown"`) {
		t.Fatalf("list_jobs = %s", raw)
	}
}
