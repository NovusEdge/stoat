package mcpsrv

import (
	"context"
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
	"github.com/novusedge/stoat/internal/sshx"
)

const jobRoot = "/run/stoat/jobs"

type execIn struct {
	VM             string            `json:"vm" jsonschema:"name of the VM"`
	Argv           []string          `json:"argv" jsonschema:"the guest command as an argv; it is never re-parsed by a shell on the host"`
	Stdin          string            `json:"stdin,omitempty" jsonschema:"data to send on the command's stdin"`
	CWD            string            `json:"cwd,omitempty" jsonschema:"absolute directory in the guest to run in"`
	Env            map[string]string `json:"env,omitempty" jsonschema:"environment variables to set for this command"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" jsonschema:"a plain count of seconds, capped at 600; 60 is the default"`
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

type jobKillIn struct {
	VM     string `json:"vm" jsonschema:"name of the VM"`
	JobID  string `json:"job_id" jsonschema:"job id returned by exec_bg"`
	Signal string `json:"signal,omitempty" jsonschema:"signal name without the SIG prefix; TERM is the default"`
}

var signalRE = regexp.MustCompile(`^[A-Z]{2,10}[0-9]?$`)

func execTimeout(seconds int) time.Duration {
	if seconds == 0 {
		return 60 * time.Second
	}
	return time.Duration(clampInt(seconds, 1, maxExecSecs)) * time.Second
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
		"Run a command inside a VM over ssh and return its stdout, stderr and exit code. argv is an argv, never a shell string, so a value with a space or a semicolon stays one word. The command runs with the guest ssh user's privileges, and effects inside the guest are whatever the command does. timeout_seconds is capped at 600. It needs agent_access exec, and it refuses when the VM is not running. It reaches outside this process.",
		func(ctx context.Context, in execIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.CommandResult{}, err
			}
			if len(in.Argv) == 0 {
				return wire.CommandResult{}, fmt.Errorf("argv is required")
			}
			argv, err := envArgv(in.Env, in.CWD, in.Argv)
			if err != nil {
				return wire.CommandResult{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, execTimeout(in.TimeoutSeconds))
			defer cancel()
			var stdin io.Reader
			if in.Stdin != "" {
				stdin = strings.NewReader(in.Stdin)
			}
			out, errb, code, err := sshx.Run(ctx, v, false, argv, stdin)
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
		"Start a command inside a VM and return at once with a job id. The command's stdout, stderr and exit code land under /run/stoat/jobs in the guest; read them with job_status and job_output. A reboot clears the guest side and job_status then reports unknown. It needs agent_access exec, and it refuses when the VM is not running. It reaches outside this process.",
		func(ctx context.Context, in execBgIn) (wire.JobStarted, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobStarted{}, err
			}
			if len(in.Argv) == 0 {
				return wire.JobStarted{}, fmt.Errorf("argv is required")
			}
			argv, err := envArgv(in.Env, in.CWD, in.Argv)
			if err != nil {
				return wire.JobStarted{}, err
			}
			id := newJobID()
			dir := path.Join(jobRoot, id)
			if _, _, code, err := sshx.Run(ctx, v, false, []string{"mkdir", "-p", dir}, nil); err != nil {
				return wire.JobStarted{}, err
			} else if code != 0 {
				if runErr := requireRunning(v); runErr != nil {
					return wire.JobStarted{}, runErr
				}
				return wire.JobStarted{}, fmt.Errorf("%s: cannot create %s", v.Name, dir)
			}
			// The runner is a constant shell body. The job directory is $1
			// and the command is the remaining positional arguments, so no
			// tool input becomes shell syntax.
			const runner = `d="$1"; shift; { "$@" >"$d/out" 2>"$d/err"; echo $? >"$d/exit"; } & echo $! >"$d/pid"`
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
		"Report a background job's state: running while its process is alive, exited with the command's exit code once it finished, or unknown when the guest side is gone, which is what a reboot leaves. It needs agent_access exec.",
		func(ctx context.Context, in jobIn) (wire.JobStatus, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobStatus{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.JobStatus{}, err
			}
			out, _, code, err := sshx.Run(ctx, v, false, []string{"cat", path.Join(j.Dir, "exit")}, nil)
			if err != nil {
				return wire.JobStatus{}, err
			}
			if code == 0 {
				exit, _ := strconv.Atoi(strings.TrimSpace(string(out)))
				return wire.JobStatus{JobID: j.ID, State: "exited", ExitCode: exit}, nil
			}
			pidOut, _, pidCode, err := sshx.Run(ctx, v, false, []string{"cat", path.Join(j.Dir, "pid")}, nil)
			if err != nil {
				return wire.JobStatus{}, err
			}
			if pidCode != 0 {
				return wire.JobStatus{JobID: j.ID, State: "unknown"}, nil
			}
			pid := strings.TrimSpace(string(pidOut))
			_, _, aliveCode, err := sshx.Run(ctx, v, false, []string{"kill", "-0", pid}, nil)
			if err != nil {
				return wire.JobStatus{}, err
			}
			if aliveCode == 0 {
				return wire.JobStatus{JobID: j.ID, State: "running"}, nil
			}
			return wire.JobStatus{JobID: j.ID, State: "unknown"}, nil
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
				return wire.FileContent{}, fmt.Errorf("invalid stream %q: stdout or stderr", in.Stream)
			}
			return readGuestFile(ctx, v, path.Join(j.Dir, file), in.Offset, in.MaxBytes)
		})

	register(server, "job_kill", classExec,
		"Send a signal to a background job's process. TERM is the default. It needs agent_access exec.",
		func(ctx context.Context, in jobKillIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.CommandResult{}, err
			}
			j, err := findJob(v.Name, in.JobID)
			if err != nil {
				return wire.CommandResult{}, err
			}
			sig := in.Signal
			if sig == "" {
				sig = "TERM"
			}
			if !signalRE.MatchString(sig) {
				return wire.CommandResult{}, fmt.Errorf("invalid signal %q: a name such as TERM or KILL, without the SIG prefix", sig)
			}
			pidOut, _, code, err := sshx.Run(ctx, v, false, []string{"cat", path.Join(j.Dir, "pid")}, nil)
			if err != nil {
				return wire.CommandResult{}, err
			}
			if code != 0 {
				return wire.CommandResult{}, fmt.Errorf("job %s has no pid on the guest; a reboot clears it", j.ID)
			}
			return runToResult(ctx, v, false, []string{"kill", "-" + sig, strings.TrimSpace(string(pidOut))})
		})

	register(server, "list_jobs", classRead,
		"List the background jobs this server started in a VM: id, argv, guest user, working directory and start time. It reads the host's own registry, so it answers without ssh and works on a stopped VM. It needs agent_access exec. Read-only.",
		func(ctx context.Context, in vmIn) (wire.JobList, error) {
			v, err := guestVM(in.VM, LevelExec)
			if err != nil {
				return wire.JobList{}, err
			}
			jobs, err := loadJobs(v.Name)
			if err != nil {
				return wire.JobList{}, err
			}
			out := wire.JobList{Jobs: []wire.Job{}}
			for _, j := range jobs {
				out.Jobs = append(out.Jobs, wire.Job{
					JobID: j.ID, Argv: j.Argv, User: j.User, CWD: j.CWD, Started: j.Started,
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
		return job{}, fmt.Errorf("no job %q on vm %q", jid, vm)
	}
	return j, nil
}
