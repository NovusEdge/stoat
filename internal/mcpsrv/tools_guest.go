package mcpsrv

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
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

type writeFileIn struct {
	VM      string `json:"vm" jsonschema:"name of the VM"`
	Path    string `json:"path" jsonschema:"absolute path in the guest; the parent directory must already exist"`
	Content string `json:"content" jsonschema:"the file's new content"`
	Mode    string `json:"mode,omitempty" jsonschema:"octal file mode such as 0644, which is the default"`
	Append  bool   `json:"append,omitempty" jsonschema:"append instead of replacing the file"`
}

type copyIn struct {
	VM     string `json:"vm" jsonschema:"name of the VM"`
	Local  string `json:"local" jsonschema:"host path, which must resolve under this VM's own shared directory"`
	Remote string `json:"remote" jsonschema:"absolute path in the guest"`
}

type pkgInstallIn struct {
	VM       string   `json:"vm" jsonschema:"name of the VM"`
	Packages []string `json:"packages" jsonschema:"package names for the guest's own package manager"`
}

type svcIn struct {
	VM     string `json:"vm" jsonschema:"name of the VM"`
	Name   string `json:"name" jsonschema:"service name"`
	Action string `json:"action" jsonschema:"enable, start, stop or restart"`
}

type useraddIn struct {
	VM   string `json:"vm" jsonschema:"name of the VM"`
	Name string `json:"name" jsonschema:"account name to create"`
}

var svcActions = []string{"enable", "start", "stop", "restart"}

var modeRE = regexp.MustCompile(`^0?[0-7]{3}$`)

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

func (s *srv) registerGuestWrite(server *mcp.Server) {
	register(server, "write_file", classExec,
		"Write a file inside a VM's guest filesystem over ssh. The path must be absolute and its parent directory must already exist. mode defaults to 0644. With append=true the content is added to the end instead of replacing the file. It needs agent_access manage or higher, and it refuses when the VM is not running. It overwrites whatever was there, and that is not reversible from here. It reaches outside this process.",
		func(ctx context.Context, in writeFileIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelManage)
			if err != nil {
				return wire.CommandResult{}, err
			}
			path, err := checkGuestPath(in.Path)
			if err != nil {
				return wire.CommandResult{}, err
			}
			mode := in.Mode
			if mode == "" {
				mode = "0644"
			}
			if !modeRE.MatchString(mode) {
				return wire.CommandResult{}, fmt.Errorf("invalid mode %q: three or four octal digits", mode)
			}
			// tee rather than a redirect: a redirect is shell syntax the
			// tool would have to build around the path.
			argv := []string{"tee", path}
			if in.Append {
				argv = []string{"tee", "-a", path}
			}
			_, errb, code, err := sshx.Run(ctx, v, true, argv, strings.NewReader(in.Content))
			if err != nil {
				return wire.CommandResult{}, err
			}
			if code != 0 {
				return wire.CommandResult{}, fmt.Errorf("%s: %s", v.Name, strings.TrimSpace(string(errb)))
			}
			return runToResult(ctx, v, true, []string{"chmod", mode, path})
		})

	register(server, "copy_to", classExec,
		"Copy a file from the host into a VM's guest filesystem. The host path must resolve under that VM's own shared directory, and anything else is refused before stoat runs. It needs agent_access manage or higher. It overwrites whatever was at the guest destination, and that is not reversible from here. It reaches outside this process.",
		s.copyHandler(true))

	register(server, "copy_from", classExec,
		"Copy a file out of a VM's guest filesystem to the host. The host destination must resolve under that VM's own shared directory, and anything else is refused before stoat runs. It needs agent_access manage or higher. It overwrites whatever was at the host destination, and that is not reversible from here. It reaches outside this process.",
		s.copyHandler(false))

	register(server, "pkg_install", classExec,
		"Install packages in a VM with the guest's own package manager, taken from the guest definition. It refreshes the package index first. Package names are passed as positional arguments and a name that reads as a flag is refused. It needs agent_access manage or higher. It reaches outside this process, since the package manager downloads.",
		func(ctx context.Context, in pkgInstallIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelManage)
			if err != nil {
				return wire.CommandResult{}, err
			}
			if len(in.Packages) == 0 {
				return wire.CommandResult{}, fmt.Errorf("packages is required")
			}
			if err := checkFlagFree(in.Packages, "packages"); err != nil {
				return wire.CommandResult{}, err
			}
			os, ok := guest.Lookup(v.OS)
			if !ok {
				return wire.CommandResult{}, fmt.Errorf("unknown guest %q; run stoat guest ls", v.OS)
			}
			// pkg.setup is the distro's own index refresh and carries no
			// tool input, so running it as the guest file wrote it is safe.
			if _, _, code, err := sshx.Run(ctx, v, true, []string{"sh", "-c", os.Pkg.Setup}, nil); err != nil {
				return wire.CommandResult{}, err
			} else if code != 0 {
				return wire.CommandResult{}, fmt.Errorf("%s: package index refresh exited %d", v.Name, code)
			}
			return runToResult(ctx, v, true, append(append([]string{}, os.Pkg.Install...), in.Packages...))
		})

	register(server, "svc", classExec,
		"Enable, start, stop or restart a service in a VM, using the init system's own verb from the guest definition. The service name is passed as a positional argument, never as shell syntax. It needs agent_access manage or higher.",
		func(ctx context.Context, in svcIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelManage)
			if err != nil {
				return wire.CommandResult{}, err
			}
			if !slices.Contains(svcActions, in.Action) {
				return wire.CommandResult{}, fmt.Errorf("invalid action %q: one of enable, start, stop, restart", in.Action)
			}
			name, err := checkSvcName(in.Name)
			if err != nil {
				return wire.CommandResult{}, err
			}
			argv, err := svcArgv(v, in.Action, name)
			if err != nil {
				return wire.CommandResult{}, err
			}
			return runToResult(ctx, v, true, argv)
		})

	register(server, "useradd", classExec,
		"Create an account in a VM, using the guest definition's own useradd verb. The account name is passed as a positional argument. It needs agent_access manage or higher.",
		func(ctx context.Context, in useraddIn) (wire.CommandResult, error) {
			v, err := guestVM(in.VM, LevelManage)
			if err != nil {
				return wire.CommandResult{}, err
			}
			name, err := checkSvcName(in.Name)
			if err != nil {
				return wire.CommandResult{}, err
			}
			o, ok := guest.Lookup(v.OS)
			if !ok || o.Cmd["useradd"] == "" {
				return wire.CommandResult{}, fmt.Errorf("guest %q declares no cmd.useradd", v.OS)
			}
			return runToResult(ctx, v, true,
				[]string{"sh", "-c", renderVerb(o.Cmd["useradd"]), "stoat_useradd", name})
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

// copyHandler builds copy_to and copy_from. They differ only in direction,
// and both confine the host side to the VM's own shared directory.
func (s *srv) copyHandler(toRemote bool) func(context.Context, copyIn) (wire.CopyResult, error) {
	return func(ctx context.Context, in copyIn) (wire.CopyResult, error) {
		name, err := checkVMName(in.VM)
		if err != nil {
			return wire.CopyResult{}, err
		}
		if err := requireAccess(name, LevelManage); err != nil {
			return wire.CopyResult{}, err
		}
		local, err := checkHostPath(in.Local, name)
		if err != nil {
			return wire.CopyResult{}, err
		}
		remote, err := checkGuestPath(in.Remote)
		if err != nil {
			return wire.CopyResult{}, err
		}
		if toRemote {
			err = core.CopyTo(ctx, name, local, remote)
		} else {
			err = core.CopyFrom(ctx, name, remote, local)
		}
		if err != nil {
			return wire.CopyResult{}, err
		}
		// The result echoes the host path this server authorised. The 9p
		// mapped-xattr defence and this guard are independent, and neither
		// covers the other.
		return wire.CopyResult{VM: name, Local: local, Remote: remote, ToRemote: toRemote}, nil
	}
}
