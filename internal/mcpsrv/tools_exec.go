package mcpsrv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/sshx"
)

const (
	jobRoot      = "/run/stoat/jobs"
	jobTailBytes = 4096
)

// jobPollInterval is how often job_wait re-reads a job's state. A variable so
// a test does not wait a second per poll.
var jobPollInterval = time.Second

type execIn struct {
	VM             string            `json:"vm" jsonschema:"name of the VM"`
	Argv           []string          `json:"argv" jsonschema:"the guest command as an argv; it is never re-parsed by a shell on the host"`
	Stdin          string            `json:"stdin,omitempty" jsonschema:"data to send on the command's stdin"`
	CWD            string            `json:"cwd,omitempty" jsonschema:"absolute directory in the guest to run in"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"environment variables to set for this command"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" jsonschema:"a plain count of seconds, capped at 600; 60 is the default. On timeout the call still returns the partial output with timed_out=true, and the guest kills the command"`
}

type execBgIn struct {
	VM   string            `json:"vm" jsonschema:"name of the VM"`
	Argv []string          `json:"argv" jsonschema:"the guest command as an argv"`
	CWD  string            `json:"cwd,omitempty" jsonschema:"absolute directory in the guest to run in"`
	Env  map[string]string `json:"env,omitempty" jsonschema:"environment variables to set for this command"`
}

type jobIn struct {
	VM    string `json:"vm" jsonschema:"name of the VM"`
	JobID string `json:"job_id" jsonschema:"job id returned by exec_bg"`
}

type jobOutputIn struct {
	VM       string `json:"vm" jsonschema:"name of the VM"`
	JobID    string `json:"job_id" jsonschema:"job id returned by exec_bg"`
	Stream   string `json:"stream,omitempty" jsonschema:"stdout or stderr; stdout is the default"`
	Offset   int    `json:"offset,omitempty" jsonschema:"byte offset to start at"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"how many bytes to read, capped at 1048576"`
}

type jobWaitIn struct {
	VM             string `json:"vm" jsonschema:"name of the VM"`
	JobID          string `json:"job_id" jsonschema:"job id returned by exec_bg"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"a plain count of seconds, capped at 600; 60 is the default"`
}

type jobKillIn struct {
	VM     string `json:"vm" jsonschema:"name of the VM"`
	JobID  string `json:"job_id" jsonschema:"job id returned by exec_bg"`
	Signal string `json:"signal,omitempty" jsonschema:"signal name such as TERM, HUP or KILL, with or without the SIG prefix; TERM is the default"`
}

var signalRE = regexp.MustCompile(`^[A-Z]{2,10}[0-9]?$`)

func execTimeout(seconds int) time.Duration {
	if seconds == 0 {
		return 60 * time.Second
	}
	return time.Duration(clampInt(seconds, 1, maxExecSecs)) * time.Second
}

// withGuestTimeout runs argv under the guest's timeout(1) when it has one.
// Killing the local ssh does not stop the remote command: without a pty, sshd
// only closes its pipes, and a command that never writes keeps running. The
// guest-side limit is the only thing that ends it. KILL is used because
// busybox and coreutils both accept -s and only coreutils accepts -k; a guest
// with no timeout runs the command unguarded.
func withGuestTimeout(limit time.Duration, argv []string) []string {
	const guard = `s="$1"; shift; if command -v timeout >/dev/null 2>&1; then exec timeout -s KILL "$s" "$@"; fi; exec "$@"`
	return append([]string{"sh", "-c", guard, "stoat_timeout", strconv.Itoa(int(limit.Seconds()))}, argv...)
}

// envArgv prefixes an argv with env so a variable is set without any shell
// syntax. Names are bounded because env itself splits on the first "=".
func envArgv(env map[string]string, cwd string, argv []string) ([]string, error) {
	out := argv
	if len(env) > 0 {
		pre := []string{"env"}
		for k, v := range env {
			if _, err := checkEnvName(k); err != nil {
				return nil, err
			}
			pre = append(pre, k+"="+v)
		}
		out = append(pre, out...)
	}
	if cwd != "" {
		p, err := checkGuestPath(cwd)
		if err != nil {
			return nil, err
		}
		// cd is a shell builtin, so sh -c is the portable spelling. The
		// directory arrives as $1 and the command as the remaining args.
		out = append([]string{"sh", "-c", `cd "$1" || exit 1; shift; exec "$@"`, "stoat_cd", p}, out...)
	}
	return out, nil
}

func (s *srv) registerExec(server *mcp.Server) {
	register(server, "exec", classExec,
		"Run a command inside a VM over ssh and return its stdout, stderr and exit code. argv is an argv, never a shell string, so a value with a space or a semicolon stays one word. The command runs with the guest ssh user's privileges, and effects inside the guest are whatever the command does. timeout_seconds is capped at 600, and a command that exceeds it returns its partial output with timed_out=true; use exec_bg for anything longer. It needs agent_access exec, and it refuses when the VM is not running. When the VM is running but sshd is not up yet it fails with cannot_reach; call wait first. It reaches outside this process.",
		func(ctx context.Context, in execIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.CommandResult{}, err
			}
			if len(in.Argv) == 0 {
				return wire.CommandResult{}, badInput("argv is required")
			}
			argv, err := envArgv(in.Env, in.CWD, in.Argv)
			if err != nil {
				return wire.CommandResult{}, err
			}
			limit := execTimeout(in.TimeoutSeconds)
			ctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			var stdin io.Reader
			if in.Stdin != "" {
				stdin = strings.NewReader(in.Stdin)
			}
			out, errb, code, err := sshx.Run(ctx, v, false, withGuestTimeout(limit, argv), stdin)
			if errors.Is(err, context.DeadlineExceeded) {
				return wire.CommandResult{
					Stdout: string(out), Stderr: string(errb), ExitCode: code, TimedOut: true,
					Message: fmt.Sprintf("timed out after %s; the output above is partial. Use exec_bg for a command that runs longer, then job_status and job_output.", limit),
				}, nil
			}
			if err != nil {
				return wire.CommandResult{}, err
			}
			if code != 0 {
				if runErr := requireRunning(v); runErr != nil {
					return wire.CommandResult{}, runErr
				}
			}
			return wire.CommandResult{Stdout: string(out), Stderr: string(errb), ExitCode: code}, nil
		})

	register(server, "exec_bg", classExec,
		"Start a command inside a VM and return at once with a job id. The command's stdout, stderr and exit code land under /run/stoat/jobs in the guest; read them with job_status and job_output. It returns before the guest records the pid, so job_status reports starting for a moment. A reboot clears the guest side and job_status then reports unknown. It needs agent_access exec, and it refuses when the VM is not running. It reaches outside this process.",
		func(ctx context.Context, in execBgIn) (wire.JobStarted, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobStarted{}, err
			}
			if len(in.Argv) == 0 {
				return wire.JobStarted{}, badInput("argv is required")
			}
			argv, err := envArgv(in.Env, in.CWD, in.Argv)
			if err != nil {
				return wire.JobStarted{}, err
			}
			id := newJobID()
			dir := path.Join(jobRoot, id)
			// /run/stoat is root-owned on every guest, so the directory is made
			// escalated and handed to the ssh user, who then runs the job.
			const mkjob = `mkdir -p "$1" && chown "$2" "$1"`
			if _, _, code, err := sshx.Run(ctx, v, true, []string{"sh", "-c", mkjob, "stoat_jobdir", dir, sshx.User(v)}, nil); err != nil {
				return wire.JobStarted{}, err
			} else if code != 0 {
				if runErr := requireRunning(v); runErr != nil {
					return wire.JobStarted{}, runErr
				}
				return wire.JobStarted{}, fmt.Errorf("%s: cannot create %s", v.Name, dir)
			}
			// The runner is a constant shell body. The job directory is $1
			// and the command is the remaining positional arguments, so no
			// tool input becomes shell syntax. nohup keeps the wrapper alive
			// after the ssh session ends; the pid file names the command
			// itself, so job_kill signals it and the wrapper records its exit.
			const runner = `d="$1"; shift; nohup sh -c 'd="$1"; shift; "$@" >"$d/out" 2>"$d/err" & c=$!; echo $c >"$d/pid"; wait $c; echo $? >"$d/exit"' stoat_job "$d" "$@" >/dev/null 2>&1 &`
			start := append([]string{"sh", "-c", runner, "stoat_job", dir}, argv...)
			if _, errb, code, err := sshx.Run(ctx, v, false, start, nil); err != nil {
				return wire.JobStarted{}, err
			} else if code != 0 {
				return wire.JobStarted{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
			}
			j := job{ID: id, Argv: in.Argv, User: sshx.User(v), CWD: in.CWD, Dir: dir, Started: time.Now().UTC()}
			if err := saveJob(v.Name, j); err != nil {
				return wire.JobStarted{}, err
			}
			return wire.JobStarted{JobID: id, Dir: dir}, nil
		})

	register(server, "job_status", classRead,
		"Report a background job's state: starting between exec_bg and the guest recording the command's pid, running while that process is alive, exited with exit_code set once it finished (exit_code is absent before that), or unknown when the guest side is gone, which is what a reboot leaves. It needs agent_access exec.",
		func(ctx context.Context, in jobIn) (wire.JobStatus, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobStatus{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.JobStatus{}, err
			}
			st, _, err := jobState(ctx, v, j)
			return st, err
		})

	register(server, "job_wait", classRead,
		"Block until a background job exits or timeout_seconds passes, then return its state, its exit code once it has one, and the last 4096 bytes of its stdout and stderr. timeout_seconds is a plain count of seconds, 60 by default and capped at 600. A job still running at the deadline is not an error: the result has timed_out set, so call job_wait again. It needs agent_access exec.",
		func(ctx context.Context, in jobWaitIn) (wire.JobWait, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobWait{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.JobWait{}, err
			}
			wctx, cancel := context.WithTimeout(ctx, jobWaitTimeout(in.TimeoutSeconds))
			defer cancel()
			st := wire.JobStatus{JobID: j.ID, State: "unknown"}
			for {
				cur, _, err := jobState(wctx, v, j)
				if err != nil {
					// The deadline killing an in-flight ssh call is how this
					// loop ends for a job that outlives it.
					if wctx.Err() != nil && ctx.Err() == nil {
						break
					}
					return wire.JobWait{}, err
				}
				st = cur
				if st.State == "exited" || st.State == "unknown" {
					break
				}
				select {
				case <-wctx.Done():
				case <-time.After(jobPollInterval):
					continue
				}
				break
			}
			return wire.JobWait{
				JobStatus: st,
				TimedOut:  st.State == "starting" || st.State == "running",
				Stdout:    jobTail(ctx, v, path.Join(j.Dir, "out")),
				Stderr:    jobTail(ctx, v, path.Join(j.Dir, "err")),
			}, nil
		})

	register(server, "job_output", classRead,
		"Read a background job's stdout or stderr. max_bytes is capped at 1048576, and binary comes back base64 encoded with encoding set. It needs agent_access exec.",
		func(ctx context.Context, in jobOutputIn) (wire.FileContent, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.FileContent{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.FileContent{}, err
			}
			file := "out"
			switch in.Stream {
			case "", "stdout":
			case "stderr":
				file = "err"
			default:
				return wire.FileContent{}, badInput("invalid stream %q: stdout or stderr", in.Stream)
			}
			return readGuestFile(ctx, v, path.Join(j.Dir, file), in.Offset, in.MaxBytes)
		})

	register(server, "job_kill", classExec,
		"Send a signal to a running background job's process. TERM is the default. A job that is not running, because it already exited or a reboot cleared it, is not signaled: the result carries its state and signaled false. A signaled job may need a moment to exit, so follow with job_wait. It needs agent_access exec.",
		func(ctx context.Context, in jobKillIn) (wire.JobKill, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobKill{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.JobKill{}, err
			}
			sig := in.Signal
			if sig == "" {
				sig = "TERM"
			}
			if !signalRE.MatchString(sig) {
				return wire.JobKill{}, badInput("invalid signal %q: a name such as TERM, HUP or KILL", sig)
			}
			st, pid, err := jobState(ctx, v, j)
			if err != nil {
				return wire.JobKill{}, err
			}
			if st.State != "running" {
				return wire.JobKill{JobStatus: st}, nil
			}
			res, err := runToResult(ctx, v, false, []string{"kill", "-" + sig, pid})
			if err != nil {
				return wire.JobKill{}, err
			}
			if res.ExitCode != 0 {
				// The job can exit between reading its state and the signal.
				if again, _, err := jobState(ctx, v, j); err == nil && again.State != "running" {
					return wire.JobKill{JobStatus: again}, nil
				}
				return wire.JobKill{}, fmt.Errorf("%s: kill %s: %s", v.Name, sig, strings.TrimSpace(res.Stderr))
			}
			return wire.JobKill{JobStatus: st, Signaled: true}, nil
		})

	register(server, "list_jobs", classRead,
		"List the background jobs this server started in a VM: id, state, exit code once exited, argv, guest user, working directory and start time. The registry is the host's own, so the list works on a stopped VM, but state needs one ssh call: state is unknown for a job whose guest files are gone, which is what a reboot leaves, and for every job when the guest does not answer. It needs agent_access exec. Read-only.",
		func(ctx context.Context, in vmIn) (wire.JobList, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobList{}, err
			}
			jobs, err := loadJobs(v.Name)
			if err != nil {
				return wire.JobList{}, err
			}
			states, err := jobStates(ctx, v, jobs)
			if err != nil {
				return wire.JobList{}, err
			}
			out := wire.JobList{Jobs: []wire.Job{}}
			for _, j := range jobs {
				st, ok := states[j.Dir]
				if !ok {
					st.State = "unknown"
				}
				out.Jobs = append(out.Jobs, wire.Job{
					JobID: j.ID, State: st.State, ExitCode: st.ExitCode,
					Argv: j.Argv, User: j.User, CWD: j.CWD, Started: j.Started,
				})
			}
			slices.SortFunc(out.Jobs, func(a, b wire.Job) int { return strings.Compare(a.JobID, b.JobID) })
			return out, nil
		})
}

func findJob(vm, id string) (job, error) {
	jid, err := checkJobID(id)
	if err != nil {
		return job{}, err
	}
	jobs, err := loadJobs(vm)
	if err != nil {
		return job{}, err
	}
	j, ok := jobs[jid]
	if !ok {
		return job{}, wire.WithSentinel(fmt.Errorf("no job %q on vm %q; list_jobs shows the ones this server started", jid, vm), core.ErrNotFound)
	}
	return j, nil
}

// jobState reads one job's guest-side state and, while it runs, its pid.
// The exit file is written last by the wrapper, so it outranks a pid whose
// process is already gone.
func jobState(ctx context.Context, v *config.VM, j job) (wire.JobStatus, string, error) {
	out, _, code, err := sshx.Run(ctx, v, false, []string{"cat", path.Join(j.Dir, "exit")}, nil)
	if err != nil {
		return wire.JobStatus{}, "", err
	}
	if code == 0 {
		exit, _ := strconv.Atoi(strings.TrimSpace(string(out)))
		return wire.JobStatus{JobID: j.ID, State: "exited", ExitCode: &exit}, "", nil
	}
	pidOut, _, pidCode, err := sshx.Run(ctx, v, false, []string{"cat", path.Join(j.Dir, "pid")}, nil)
	if err != nil {
		return wire.JobStatus{}, "", err
	}
	if pidCode != 0 {
		// No pid file has two causes, and a caller acts on them
		// differently. The job directory still being there means the
		// wrapper has not written the pid yet, which is where a
		// job_status call right after exec_bg lands. A directory that
		// is gone is what a reboot leaves.
		_, _, dirCode, err := sshx.Run(ctx, v, false, []string{"test", "-d", j.Dir}, nil)
		if err != nil {
			return wire.JobStatus{}, "", err
		}
		if dirCode == 0 {
			return wire.JobStatus{JobID: j.ID, State: "starting"}, "", nil
		}
		return wire.JobStatus{JobID: j.ID, State: "unknown"}, "", nil
	}
	pid := strings.TrimSpace(string(pidOut))
	_, _, aliveCode, err := sshx.Run(ctx, v, false, []string{"kill", "-0", pid}, nil)
	if err != nil {
		return wire.JobStatus{}, "", err
	}
	if aliveCode == 0 {
		return wire.JobStatus{JobID: j.ID, State: "running"}, pid, nil
	}
	return wire.JobStatus{JobID: j.ID, State: "unknown"}, "", nil
}

// jobStatesScript is jobState in one guest round trip for every job
// directory, which are its positional arguments. It prints dir, state and
// exit code per line, tab separated.
const jobStatesScript = `for d in "$@"; do
  if [ -f "$d/exit" ]; then printf '%s\texited\t%s\n' "$d" "$(cat "$d/exit")"
  elif [ -f "$d/pid" ]; then
    if kill -0 "$(cat "$d/pid")" 2>/dev/null; then printf '%s\trunning\t\n' "$d"
    else printf '%s\tunknown\t\n' "$d"; fi
  elif [ -d "$d" ]; then printf '%s\tstarting\t\n' "$d"
  else printf '%s\tunknown\t\n' "$d"; fi
done`

// jobStates returns the state of every job, keyed by job directory. A guest
// that does not answer leaves the map empty, which the caller reads as
// unknown: list_jobs has to work on a stopped VM.
func jobStates(ctx context.Context, v *config.VM, jobs map[string]job) (map[string]wire.JobStatus, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	argv := []string{"sh", "-c", jobStatesScript, "stoat_jobs"}
	for _, j := range jobs {
		argv = append(argv, j.Dir)
	}
	out, _, code, err := sshx.Run(ctx, v, false, argv, nil)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, nil
	}
	return parseJobStates(out), nil
}

func parseJobStates(raw []byte) map[string]wire.JobStatus {
	out := map[string]wire.JobStatus{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		st := wire.JobStatus{State: f[1]}
		if st.State == "exited" {
			exit, _ := strconv.Atoi(strings.TrimSpace(f[2]))
			st.ExitCode = &exit
		}
		out[f[0]] = st
	}
	return out
}

// jobTail returns the end of one job stream. A stream that cannot be read is
// empty, since a job that never wrote or a cleared guest has nothing to show.
func jobTail(ctx context.Context, v *config.VM, file string) string {
	out, _, code, err := sshx.Run(ctx, v, false, []string{"tail", "-c", strconv.Itoa(jobTailBytes), file}, nil)
	if err != nil || code != 0 {
		return ""
	}
	return strings.ToValidUTF8(string(out), "\uFFFD")
}

func jobWaitTimeout(seconds int) time.Duration {
	if seconds == 0 {
		return 60 * time.Second
	}
	return time.Duration(clampInt(seconds, 1, maxWaitSecs)) * time.Second
}
