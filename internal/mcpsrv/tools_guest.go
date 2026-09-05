package mcpsrv

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/sshx"
)

type readFileIn struct {
	VM       string `json:"vm" jsonschema:"name of the VM"`
	Path     string `json:"path" jsonschema:"absolute path in the guest; a relative path is refused"`
	Offset   int    `json:"offset,omitempty" jsonschema:"byte offset to start at"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"how many bytes to read, capped at 1048576"`
}

type pathIn struct {
	VM   string `json:"vm" jsonschema:"name of the VM"`
	Path string `json:"path" jsonschema:"absolute path in the guest; a relative path is refused"`
}

type svcStatusIn struct {
	VM   string `json:"vm" jsonschema:"name of the VM"`
	Name string `json:"name" jsonschema:"service name"`
}

type tailLogIn struct {
	VM    string `json:"vm" jsonschema:"name of the VM"`
	Unit  string `json:"unit,omitempty" jsonschema:"systemd unit to read with journalctl"`
	Path  string `json:"path,omitempty" jsonschema:"absolute path of a log file to tail instead of a unit"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many lines to return, capped at 2000"`
}

// readSize clamps a caller's max_bytes. 0 means the caller did not set a
// limit, which reads as "the full clamp", not "the smallest possible read".
func readSize(n int) int {
	if n <= 0 {
		return maxReadBytes
	}
	return clampInt(n, 1, maxReadBytes)
}

// guestVM resolves a VM for an in-VM tool: the name guard, the access gate,
// and the config load, in that order. Every guest tool starts here. need is
// the tool's own row in toolTable (table_test.go), which a test walks to
// check every cell against this same gate.
func guestVM(name string, need Level) (*config.VM, error) {
	n, err := checkVMName(name)
	if err != nil {
		return nil, err
	}
	if err := requireAccess(n, need); err != nil {
		return nil, err
	}
	return config.Load(n)
}

// readGuestFile reads path in v's guest, clamped to readSize(maxBytes), and
// encodes it for the wire. read_file and job_output share this: both clamp
// and encode identically, and only differ in where path comes from.
func readGuestFile(ctx context.Context, v *config.VM, path string, offset, maxBytes int) (wire.FileContent, error) {
	sizeOut, _, code, err := sshx.Run(ctx, v, false, []string{"stat", "-c", "%s", path}, nil)
	if err != nil {
		return wire.FileContent{}, err
	}
	if code != 0 {
		return wire.FileContent{}, fmt.Errorf("%s: cannot stat %s", v.Name, path)
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	n := readSize(maxBytes)

	// head -c is the fast path and every guest has it. dd with bs=1 is the
	// only portable way to skip an unaligned offset, and the clamp bounds
	// its cost.
	argv := []string{"head", "-c", strconv.Itoa(n), path}
	if offset > 0 {
		argv = []string{"dd", "if=" + path, "bs=1",
			"skip=" + strconv.Itoa(offset), "count=" + strconv.Itoa(n), "status=none"}
	}
	data, errb, code, err := sshx.Run(ctx, v, false, argv, nil)
	if err != nil {
		return wire.FileContent{}, err
	}
	if code != 0 {
		return wire.FileContent{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
	}
	out := wire.FileContent{
		Size:      size,
		Truncated: int64(offset)+int64(len(data)) < size,
	}
	if utf8.Valid(data) {
		out.Content = string(data)
	} else {
		out.Content = base64.StdEncoding.EncodeToString(data)
		out.Encoding = "base64"
	}
	return out, nil
}

func (s *srv) registerGuestRead(server *mcp.Server) {
	register(server, "read_file", classRead,
		"Read a file from a VM's guest filesystem over ssh. The path must be absolute; a relative path is refused rather than resolved against the guest user's home, so one call means the same thing on every guest. max_bytes is capped at 1048576. Text comes back in content; binary comes back base64 encoded with encoding set. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in readFileIn) (wire.FileContent, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.FileContent{}, err
			}
			path, err := checkGuestPath(in.Path)
			if err != nil {
				return wire.FileContent{}, err
			}
			return readGuestFile(ctx, v, path, in.Offset, in.MaxBytes)
		})

	register(server, "list_dir", classRead,
		"List one directory in a VM's guest filesystem: name, type, size, mode and mtime for each entry. The path must be absolute. The listing is capped at 2000 entries and truncated is set when it hits the cap. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in pathIn) (wire.DirListing, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.DirListing{}, err
			}
			path, err := checkGuestPath(in.Path)
			if err != nil {
				return wire.DirListing{}, err
			}
			// Two calls rather than find -printf: busybox find has no
			// -printf, and busybox ls and stat both behave.
			nameOut, errb, code, err := sshx.Run(ctx, v, false, []string{"ls", "-A", path}, nil)
			if err != nil {
				return wire.DirListing{}, err
			}
			if code != 0 {
				return wire.DirListing{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
			}
			names := capNames(strings.Fields(string(nameOut)))
			if len(names) == 0 {
				return wire.DirListing{Entries: []wire.DirEntry{}}, nil
			}
			argv := append([]string{"stat", "-c", "%n\t%F\t%s\t%f\t%Y"}, joinDir(path, names)...)
			statOut, _, _, err := sshx.Run(ctx, v, false, argv, nil)
			if err != nil {
				return wire.DirListing{}, err
			}
			return wire.DirListing{
				Entries:   parseStat(statOut),
				Truncated: len(names) == maxDirEntries,
			}, nil
		})

	register(server, "stat", classRead,
		"Report one path's type, size, mode and mtime in a VM's guest filesystem. The path must be absolute. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in pathIn) (wire.DirEntry, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.DirEntry{}, err
			}
			path, err := checkGuestPath(in.Path)
			if err != nil {
				return wire.DirEntry{}, err
			}
			out, errb, code, err := sshx.Run(ctx, v, false, []string{"stat", "-c", "%n\t%F\t%s\t%f\t%Y", path}, nil)
			if err != nil {
				return wire.DirEntry{}, err
			}
			if code != 0 {
				return wire.DirEntry{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
			}
			entries := parseStat(out)
			if len(entries) == 0 {
				return wire.DirEntry{}, fmt.Errorf("%s: stat returned nothing for %s", v.Name, path)
			}
			return entries[0], nil
		})

	register(server, "ps", classRead,
		"List the processes running in a VM: pid, ppid, user, elapsed time and the command. The list is capped at 2000 rows and truncated is set when it hits the cap. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in vmIn) (wire.ProcessList, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.ProcessList{}, err
			}
			out, _, code, err := sshx.Run(ctx, v, false,
				[]string{"ps", "-eo", "pid,ppid,user,etime,args"}, nil)
			if err != nil {
				return wire.ProcessList{}, err
			}
			if code != 0 {
				// busybox ps rejects -eo. Its own spelling has no etime.
				out, _, code, err = sshx.Run(ctx, v, false, []string{"ps", "-o", "pid,ppid,user,args"}, nil)
				if err != nil {
					return wire.ProcessList{}, err
				}
				if code != 0 {
					return wire.ProcessList{}, fmt.Errorf("%s: ps exited %d", v.Name, code)
				}
			}
			rows := parsePS(out)
			list := wire.ProcessList{Truncated: len(rows) > maxPSRows}
			list.Processes = rows[:min(len(rows), maxPSRows)]
			return list, nil
		})

	register(server, "svc_status", classRead,
		"Report one service's status in a VM, using the init system's own status verb from the guest definition. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in svcStatusIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.CommandResult{}, err
			}
			name, err := checkSvcName(in.Name)
			if err != nil {
				return wire.CommandResult{}, err
			}
			argv, err := svcArgv(v, "status", name)
			if err != nil {
				return wire.CommandResult{}, err
			}
			return runToResult(ctx, v, false, argv)
		})

	register(server, "tail_log", classRead,
		"Tail a log in a VM: a systemd unit's journal with unit, or a log file with path. Without either, it reads the init system's own log path from the guest definition. The line count is capped at 2000. It needs agent_access observe or higher. Read-only in the guest.",
		func(ctx context.Context, in tailLogIn) (wire.LogTail, error) {
			v, err := guestVM(in.VM, LevelObserve)
			if err != nil {
				return wire.LogTail{}, err
			}
			n := clampInt(in.Lines, 1, maxLogLines)
			var argv []string
			switch {
			case in.Path != "":
				path, err := checkGuestPath(in.Path)
				if err != nil {
					return wire.LogTail{}, err
				}
				argv = []string{"tail", "-n", strconv.Itoa(n), path}
			case in.Unit != "":
				unit, err := checkSvcName(in.Unit)
				if err != nil {
					return wire.LogTail{}, err
				}
				os, ok := guest.Lookup(v.OS)
				if !ok || os.Init != "systemd" {
					return wire.LogTail{}, fmt.Errorf("%s: unit needs a systemd guest; pass path instead", v.Name)
				}
				argv = []string{"journalctl", "-u", unit, "-n", strconv.Itoa(n), "--no-pager"}
			default:
				os, ok := guest.Lookup(v.OS)
				if !ok || os.LogPath == "" {
					return wire.LogTail{}, fmt.Errorf("%s: guest %q declares no log path; pass unit or path", v.Name, v.OS)
				}
				argv = []string{"tail", "-n", strconv.Itoa(n), os.LogPath}
			}
			out, errb, code, err := sshx.Run(ctx, v, true, argv, nil)
			if err != nil {
				return wire.LogTail{}, err
			}
			if code != 0 {
				return wire.LogTail{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
			}
			return wire.LogTail{Lines: strings.Split(strings.TrimRight(string(out), "\n"), "\n")}, nil
		})
}

// svcArgv renders the guest file's [svc] template and passes the service
// name as $1. The template is the constant and the name is a positional
// argument, so no tool input reaches the guest shell as syntax.
//
// A guest stoat does not know still gets a template: systemctl is the init
// system most guests run, the same "assume the most common answer" rule
// sshx.escalate applies for an unrecognized OS's privilege escalation.
func svcArgv(v *config.VM, action, name string) ([]string, error) {
	tmpl := "systemctl " + action + " {name}"
	if os, ok := guest.Lookup(v.OS); ok {
		if t := os.Svc.Get(action); t != "" {
			tmpl = t
		}
	}
	return []string{"sh", "-c", renderVerb(tmpl), "stoat_svc", name}, nil
}

// renderVerb turns a guest-file template into a shell body: {name} becomes
// "$1", and a template with no {name} gets "$@" appended. It repeats
// internal/guest's own shTemplate, which is unexported. The two must stay
// byte-identical.
func renderVerb(tmpl string) string {
	if strings.Contains(tmpl, "{name}") {
		return strings.ReplaceAll(tmpl, "{name}", `"$1"`)
	}
	return tmpl + ` "$@"`
}

func runToResult(ctx context.Context, v *config.VM, root bool, argv []string) (wire.CommandResult, error) {
	out, errb, code, err := sshx.Run(ctx, v, root, argv, nil)
	if err != nil {
		return wire.CommandResult{}, err
	}
	return wire.CommandResult{Stdout: string(out), Stderr: string(errb), ExitCode: code}, nil
}

func capNames(names []string) []string {
	return names[:min(len(names), maxDirEntries)]
}

func joinDir(dir string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.TrimRight(dir, "/") + "/" + n
	}
	return out
}

// parseStat reads the tab separated rows stat -c produced. A name with a tab
// in it is not representable here and stat itself has the same limit.
func parseStat(raw []byte) []wire.DirEntry {
	var out []wire.DirEntry
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		mtime, _ := strconv.ParseInt(f[4], 10, 64)
		out = append(out, wire.DirEntry{
			Name: f[0], Type: f[1], Size: size, Mode: f[3], MTime: mtime,
		})
	}
	return out
}

func parsePS(raw []byte) []wire.Process {
	var out []wire.Process
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] == "PID" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		p := wire.Process{PID: pid, PPID: ppid, User: f[2]}
		if len(f) >= 5 {
			p.Elapsed, p.Command = f[3], strings.Join(f[4:], " ")
		} else {
			p.Command = strings.Join(f[3:], " ")
		}
		out = append(out, p)
	}
	return out
}
