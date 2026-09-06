package filelock_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/filelock"
)

const helperEnv = "STOAT_FILELOCK_HELPER"

type lockHelper struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      <-chan string
	path        string
	stdinClosed bool
	waited      bool
}

func TestExclusiveNonBlockingContenderIsRefused(t *testing.T) {
	holder := startHelper(t, newLockFile(t), filelock.Exclusive, false, true)
	defer releaseHelper(t, holder)

	output, err := runHelper(holder.path, filelock.Exclusive, true, false)
	if err != nil {
		t.Fatalf("contender helper failed: output=%q, err=%v", output, err)
	}
	if got, want := strings.TrimSpace(output), "would-block"; got != want {
		t.Fatalf("contender result = %q, want %q", got, want)
	}
}

func TestSharedHoldersCoexistAndRefuseExclusiveContender(t *testing.T) {
	path := newLockFile(t)
	first := startHelper(t, path, filelock.Shared, false, true)
	defer releaseHelper(t, first)
	second := startHelper(t, path, filelock.Shared, false, true)
	defer releaseHelper(t, second)

	output, err := runHelper(path, filelock.Exclusive, true, false)
	if err != nil {
		t.Fatalf("exclusive contender helper failed: output=%q, err=%v", output, err)
	}
	if got, want := strings.TrimSpace(output), "would-block"; got != want {
		t.Fatalf("exclusive contender result = %q, want %q", got, want)
	}
}

func TestUnlockPermitsImmediateReacquisition(t *testing.T) {
	path := newLockFile(t)
	f := openLockFile(t, path)
	if err := filelock.Lock(f, filelock.Exclusive, false); err != nil {
		t.Fatalf("initial lock: %v", err)
	}
	if err := filelock.Unlock(f); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	if err := filelock.Lock(f, filelock.Exclusive, true); err != nil {
		t.Fatalf("reacquire after unlock: %v", err)
	}
	if err := filelock.Unlock(f); err != nil {
		t.Fatalf("final unlock: %v", err)
	}
}

func TestHelperProcessExitReleasesLock(t *testing.T) {
	path := newLockFile(t)
	holder := startHelper(t, path, filelock.Exclusive, false, true)
	holderExited := false
	t.Cleanup(func() {
		if !holderExited {
			releaseHelper(t, holder)
		}
	})
	if err := holder.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill lock holder: %v", err)
	}
	waitHelper(t, holder)
	holderExited = true

	f := openLockFile(t, path)
	defer f.Close()
	if err := filelock.Lock(f, filelock.Exclusive, true); err != nil {
		t.Fatalf("lock after holder exit: %v", err)
	}
	if err := filelock.Unlock(f); err != nil {
		t.Fatalf("unlock after holder exit: %v", err)
	}
}

func TestBlockingWaiterProgressesAfterRelease(t *testing.T) {
	path := newLockFile(t)
	holder := startHelper(t, path, filelock.Exclusive, false, true)
	defer releaseHelper(t, holder)

	waiter := startRawHelper(t, path, filelock.Exclusive, false, true)
	defer releaseHelper(t, waiter)
	if line, ok := readHelperLine(waiter.stdout, 150*time.Millisecond); ok {
		t.Fatalf("blocking waiter entered before release with result %q", line)
	}

	closeHelperInput(t, holder)
	if line, ok := readHelperLine(waiter.stdout, 2*time.Second); !ok || line != "locked" {
		t.Fatalf("blocking waiter result after release = %q, ok=%t; want locked", line, ok)
	}
	closeHelperInput(t, waiter)
	waitHelper(t, waiter)
}

func TestFileLockHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	if len(os.Args) < 7 || os.Args[len(os.Args)-5] != "--" {
		fmt.Fprintln(os.Stdout, "error: malformed helper arguments")
		os.Exit(2)
	}
	args := os.Args[len(os.Args)-4:]
	path, modeName := args[0], args[1]
	nonBlocking, err := strconv.ParseBool(args[2])
	if err != nil {
		fmt.Fprintf(os.Stdout, "error: parse nonBlocking: %v\n", err)
		os.Exit(2)
	}
	hold, err := strconv.ParseBool(args[3])
	if err != nil {
		fmt.Fprintf(os.Stdout, "error: parse hold: %v\n", err)
		os.Exit(2)
	}
	mode := filelock.Exclusive
	if modeName == "shared" {
		mode = filelock.Shared
	} else if modeName != "exclusive" {
		fmt.Fprintf(os.Stdout, "error: unknown mode %q\n", modeName)
		os.Exit(2)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stdout, "error: open: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()
	if err := filelock.Lock(f, mode, nonBlocking); err != nil {
		if errors.Is(err, filelock.ErrWouldBlock) {
			fmt.Fprintln(os.Stdout, "would-block")
			return
		}
		fmt.Fprintf(os.Stdout, "error: lock: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stdout, "locked")
	if hold {
		_, _ = io.Copy(io.Discard, os.Stdin)
	} else {
		fmt.Fprintln(os.Stdout, "entered")
	}
	_ = filelock.Unlock(f)
}

func newLockFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "lock")
}

func openLockFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func startHelper(t *testing.T, path string, mode filelock.Mode, nonBlocking, hold bool) *lockHelper {
	t.Helper()
	helper := startRawHelper(t, path, mode, nonBlocking, hold)
	line, ok := readHelperLine(helper.stdout, 2*time.Second)
	if !ok || line != "locked" {
		closeHelperInput(t, helper)
		waitHelper(t, helper)
		t.Fatalf("lock helper result = %q, ok=%t; want locked", line, ok)
	}
	return helper
}

func startRawHelper(t *testing.T, path string, mode filelock.Mode, nonBlocking, hold bool) *lockHelper {
	t.Helper()
	stdin, err := startCommand(t, path, mode, nonBlocking, hold)
	if err != nil {
		t.Fatal(err)
	}
	return stdin
}

func startCommand(t *testing.T, path string, mode filelock.Mode, nonBlocking, hold bool) (*lockHelper, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestFileLockHelperProcess", "--", path, modeName(mode), strconv.FormatBool(nonBlocking), strconv.FormatBool(hold))
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create helper stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create helper stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start helper: %w", err)
	}
	lines := make(chan string)
	go func() {
		defer close(lines)
		for scanner := bufio.NewScanner(stdout); scanner.Scan(); {
			lines <- strings.TrimSpace(scanner.Text())
		}
	}()
	return &lockHelper{cmd: cmd, stdin: stdin, stdout: lines, path: path}, nil
}

func runHelper(path string, mode filelock.Mode, nonBlocking, hold bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestFileLockHelperProcess", "--", path, modeName(mode), strconv.FormatBool(nonBlocking), strconv.FormatBool(hold))
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func modeName(mode filelock.Mode) string {
	if mode == filelock.Shared {
		return "shared"
	}
	return "exclusive"
}

func releaseHelper(t *testing.T, helper *lockHelper) {
	t.Helper()
	closeHelperInput(t, helper)
	waitHelper(t, helper)
}

func closeHelperInput(t *testing.T, helper *lockHelper) {
	t.Helper()
	if helper.stdinClosed {
		return
	}
	if err := helper.stdin.Close(); err != nil {
		t.Errorf("release lock helper: %v", err)
	}
	helper.stdinClosed = true
}

func waitHelper(t *testing.T, helper *lockHelper) {
	t.Helper()
	if helper.waited {
		return
	}
	if err := helper.cmd.Wait(); err != nil {
		t.Errorf("lock helper exit: %v", err)
	}
	helper.waited = true
}

func readHelperLine(lines <-chan string, timeout time.Duration) (string, bool) {
	select {
	case line, ok := <-lines:
		return line, ok && line != ""
	case <-time.After(timeout):
		return "", false
	}
}
