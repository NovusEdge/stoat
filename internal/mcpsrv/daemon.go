package mcpsrv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/filelock"
)

// DefaultAddr is the address `mcp up` binds when the caller names none.
const DefaultAddr = "127.0.0.1:7777"

// Daemon is one supervised HTTP server: the child process, the address it
// serves, and the project directory it is scoped to. The directory is part of
// the identity because the server reads the stoat.toml of its working
// directory, so two projects need two servers.
type Daemon struct {
	Dir     string    `json:"dir"`
	Addr    string    `json:"addr"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Log     string    `json:"log"`
}

// ErrNotRunning is returned when no supervised server matches the request.
var ErrNotRunning = errors.New("no mcp server is running for this project")

// executable names the binary `mcp up` re-launches. The daemon test replaces
// it with the test binary, which serves HTTP in a helper mode.
var executable = os.Executable

// stateDir holds one JSON file and one log file per project. It sits beside the
// VM directories under the data root, and is created on demand: EnsureRoot
// refuses non-Linux hosts, and the MCP server runs on all of them.
func stateDir() string { return filepath.Join(config.Root(), "mcp") }

// key identifies a project by its absolute directory. A hash keeps the
// filename flat and safe on every filesystem; the directory itself is stored
// inside the file for display.
func key(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return hex.EncodeToString(sum[:6])
}

func statePath(dir string) string { return filepath.Join(stateDir(), key(dir)+".json") }
func logPath(dir string) string   { return filepath.Join(stateDir(), key(dir)+".log") }

// lock serialises up and down across processes. Reading the record, starting
// the child and writing the record back are three steps, and two `mcp up`
// calls interleaved in that gap both start a server while only the last
// record survives.
//
// It is its own file rather than config.Lock: that one calls config.EnsureRoot,
// which refuses a host with no local hypervisor, and the MCP server runs on
// macOS and Windows.
func lock() (func(), error) {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(stateDir(), ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := filelock.Lock(f, filelock.Exclusive, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = filelock.Unlock(f)
		_ = f.Close()
	}, nil
}

// Up starts a detached HTTP server for the current directory and records it.
// It waits for the address to answer, so a failure to bind surfaces here
// instead of in a log file the caller never reads.
func Up(dir, addr string) (Daemon, error) {
	if addr == "" {
		addr = DefaultAddr
	}
	if err := CheckLoopback(addr); err != nil {
		return Daemon{}, err
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Daemon{}, err
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return Daemon{}, err
	}
	unlock, err := lock()
	if err != nil {
		return Daemon{}, err
	}
	defer unlock()

	if d, ok := lookup(dir); ok {
		return Daemon{}, fmt.Errorf("mcp server already running for %s on %s (pid %d)", d.Dir, d.Addr, d.PID)
	}
	// Claim the port before the child exists. Without this, a second project
	// running `mcp up` on an address another project already serves would find
	// the dial in waitReady answered by that other server, and record a
	// daemon its own child never started.
	probe, err := net.Listen("tcp", addr)
	if err != nil {
		return Daemon{}, fmt.Errorf("%s is in use: %w", addr, err)
	}
	if err := probe.Close(); err != nil {
		return Daemon{}, err
	}
	exe, err := executable()
	if err != nil {
		return Daemon{}, err
	}
	lp := logPath(dir)
	lf, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return Daemon{}, err
	}
	defer func() { _ = lf.Close() }()

	cmd := exec.Command(exe, "mcp", "serve", "--http", addr)
	cmd.Dir = dir
	cmd.Stdout = lf
	cmd.Stderr = lf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return Daemon{}, err
	}
	// Release the child: nothing reaps it after this process exits, and an
	// unwaited child would stay a zombie for the life of this shell.
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()

	if err := waitReady(addr, pid, 5*time.Second); err != nil {
		_ = terminate(pid)
		return Daemon{}, fmt.Errorf("%w (see %s)", err, lp)
	}
	d := Daemon{Dir: dir, Addr: addr, PID: pid, Started: time.Now().UTC(), Log: lp}
	if err := write(d); err != nil {
		_ = terminate(pid)
		return Daemon{}, err
	}
	return d, nil
}

// Down stops the server for a directory and forgets it.
func Down(dir string) (Daemon, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Daemon{}, err
	}
	unlock, err := lock()
	if err != nil {
		return Daemon{}, err
	}
	defer unlock()

	d, ok := read(dir)
	if !ok {
		return Daemon{}, ErrNotRunning
	}
	// running() proves the pid still serves this record's address, which is
	// the identity check: a reused pid belongs to an unrelated process, and
	// signalling it would kill a stranger.
	if !running(d) {
		_ = os.Remove(statePath(dir))
		return d, nil
	}
	if err := terminate(d.PID); err != nil {
		return d, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(d.PID) {
			// The record outlives a failed stop on purpose: `status` must keep
			// naming a server that is still up.
			_ = os.Remove(statePath(dir))
			return d, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return d, fmt.Errorf("mcp server pid %d did not exit", d.PID)
}

// Status lists the supervised servers, newest first, and prunes the records
// whose process is gone.
func Status() ([]Daemon, error) {
	entries, err := os.ReadDir(stateDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Held because this prunes: without it, a record written by an `mcp up`
	// mid-flight elsewhere could be read half-written and deleted.
	unlock, err := lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var out []Daemon
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		d, err := load(filepath.Join(stateDir(), e.Name()))
		if err != nil {
			continue
		}
		if !running(d) {
			_ = os.Remove(filepath.Join(stateDir(), e.Name()))
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out, nil
}

// lookup returns the live server for a directory.
func lookup(dir string) (Daemon, bool) {
	d, ok := read(dir)
	if !ok {
		return Daemon{}, false
	}
	if !running(d) {
		_ = os.Remove(statePath(dir))
		return Daemon{}, false
	}
	return d, true
}

func read(dir string) (Daemon, bool) {
	d, err := load(statePath(dir))
	if err != nil {
		return Daemon{}, false
	}
	return d, true
}

func load(path string) (Daemon, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Daemon{}, err
	}
	var d Daemon
	if err := json.Unmarshal(b, &d); err != nil {
		return Daemon{}, err
	}
	if d.PID == 0 || d.Addr == "" {
		return Daemon{}, errors.New("incomplete mcp daemon record")
	}
	return d, nil
}

// running checks the pid and the address together. Pids get reused, and a
// reused pid would report a ghost server that answers nothing; a live pid whose
// port is silent means the server died without clearing its record.
func running(d Daemon) bool {
	if !alive(d.PID) {
		return false
	}
	c, err := net.DialTimeout("tcp", d.Addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitReady(addr string, pid int, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return nil
		}
		if !alive(pid) {
			return fmt.Errorf("mcp server exited before it served %s", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("mcp server did not answer on %s", addr)
}

func write(d Daemon) error {
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	path := statePath(d.Dir)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcp-daemon-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
