package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesLogAndAppends(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	if err := Init(); err != nil {
		t.Fatal(err)
	}
	L().Info("first message", "vm", "alpha")
	if err := Close(); err != nil {
		t.Fatal(err)
	}

	if Path() != filepath.Join(root, "logs", "stoat.log") {
		t.Errorf("unexpected path %q", Path())
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "first message") || !strings.Contains(string(b), "alpha") {
		t.Errorf("log missing content: %q", b)
	}

	// A second Init must append, not truncate.
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	L().Info("second message")
	_ = Close()
	b, _ = os.ReadFile(Path())
	if !strings.Contains(string(b), "first message") {
		t.Error("Init truncated an existing log")
	}
	if !strings.Contains(string(b), "second message") {
		t.Error("second message not written")
	}
}

func TestTeeWritesToBothTargets(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	var term strings.Builder
	Tee(&term)
	L().Info("teed message", "vm", "beta")
	if err := Close(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(term.String(), "teed message") {
		t.Errorf("tee target missing the line: %q", term.String())
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "teed message") {
		t.Errorf("log file missing the line: %q", b)
	}
}

func TestTeeBeforeInitIsIgnored(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	_ = Close()
	var term strings.Builder
	Tee(&term)
	L().Info("discarded")
	if term.Len() != 0 {
		t.Errorf("Tee attached with no log file open: %q", term.String())
	}
}

func TestLBeforeInitDoesNotPanic(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	// Deliberately no Init: a stray log call must never take down the TUI.
	L().Info("this should be discarded safely")
}
