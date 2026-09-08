// Package logx provides file logging to <root>/logs/stoat.log.
//
// The log is an append-only history. <vmdir>/last-provision.log is
// truncated per run, since it captures only the last run; this file
// accumulates across the tool's lifetime.
//
// ponytail: no rotation. A personal tool's log grows slowly, so a rotation
// dependency is unjustified until someone hits a size problem. If that day
// comes, reach for lumberjack or a simple size-check-and-truncate-on-Init
// before adding anything heavier.
package logx

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"charm.land/log/v2"

	"github.com/novusedge/stoat/internal/config"
)

var (
	mu     sync.Mutex
	logger = newLogger(io.Discard)
	file   *os.File
	path   string
)

func newLogger(w io.Writer) *log.Logger {
	return log.NewWithOptions(w, log.Options{
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
		Level:           log.DebugLevel,
	})
}

// Init creates <root>/logs/, opens stoat.log for append, and configures
// the logger to write to it. Calling Init again (e.g. across test runs
// or a restart) reopens the same file in append mode.
func Init() error {
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Join(config.Root(), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "stoat.log")

	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	if file != nil {
		_ = file.Close()
	}
	file = f
	path = p
	logger = newLogger(file)
	return nil
}

// Tee sends the log to w as well as to the file. A long-running foreground
// command calls it so the terminal shows what the file records. Init resets
// the logger to the file alone, so call Tee after Init, not before.
func Tee(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil || w == nil {
		return
	}
	logger = newLogger(io.MultiWriter(file, w))
}

// L returns the configured logger. Safe to call before Init: it falls
// back to a discarding logger so a stray log call can never panic the TUI.
func L() *log.Logger {
	mu.Lock()
	defer mu.Unlock()
	return logger
}

// Path returns <root>/logs/stoat.log. Empty until Init has run.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Close closes the underlying log file, if open.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return nil
	}
	err := file.Close()
	file = nil
	return err
}
