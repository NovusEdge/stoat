package wire

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/qemu"
)

func TestMapErrorEveryCoreSentinel(t *testing.T) {
	tests := []struct {
		err  error
		code Code
	}{
		{fmt.Errorf("%w: work", core.ErrNotFound), CodeNotFound},
		{fmt.Errorf("%w: work: parse fail", core.ErrBroken), CodeBroken},
		{fmt.Errorf("%w: work", core.ErrNameTaken), CodeNameTaken},
		{fmt.Errorf("%w: name is required", core.ErrInvalidSpec), CodeInvalidSpec},
		{fmt.Errorf("%w: alpine-virt", core.ErrImageNotDownloaded), CodeImageNotDownloaded},
		{fmt.Errorf("%w: recipe %q", core.ErrRecipeNotApplicable, "xfce"), CodeRecipeNotApplicable},
		{fmt.Errorf("%w: work", core.ErrNotRunning), CodeNotRunning},
		{fmt.Errorf("%w: work", core.ErrAlreadyRunning), CodeAlreadyRunning},
		{fmt.Errorf("%w: work", core.ErrNoDisk), CodeNoDisk},
		{fmt.Errorf("%w: os", core.ErrImmutableField), CodeImmutableField},
		{fmt.Errorf("%w: 8G -> 4G", core.ErrDiskShrink), CodeDiskShrink},
		{fmt.Errorf("%w: work: not running", core.ErrCannotReach), CodeCannotReach},
		{fmt.Errorf("%w: %q", core.ErrUnknownWhich, "bogus"), CodeUnknownLog},
		{context.DeadlineExceeded, CodeTimeout},
		{context.Canceled, CodeCanceled},
		{ErrConfirmationRequired, CodeConfirmationRequired},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			got := MapError(tt.err)
			if got == nil {
				t.Fatal("MapError returned nil for a non-nil error")
			}
			if got.Code != tt.code {
				t.Errorf("code = %q, want %q", got.Code, tt.code)
			}
			if got.Message != tt.err.Error() {
				t.Errorf("message = %q, want %q", got.Message, tt.err.Error())
			}
		})
	}
}

func TestMapErrorUnrecognizedFallsBackToInternal(t *testing.T) {
	got := MapError(errors.New("qemu-img: some unrelated failure"))
	if got.Code != CodeInternal {
		t.Errorf("code = %q, want %q", got.Code, CodeInternal)
	}
	if got.Message != "qemu-img: some unrelated failure" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestMapErrorNilIsNil(t *testing.T) {
	if got := MapError(nil); got != nil {
		t.Errorf("MapError(nil) = %+v, want nil", got)
	}
}

// TestUpdateDiskGrowWhileRunningReportsAlreadyRunning pins the one ordering
// case §2 calls out: Update's disk-grow refusal on a running VM wraps
// ErrAlreadyRunning directly, never ErrDiskShrink. The wire code must be
// already_running: "stop it first", not a disk-specific code.
func TestUpdateDiskGrowWhileRunningReportsAlreadyRunning(t *testing.T) {
	err := fmt.Errorf("%w: disk: stop work before resizing its disk", core.ErrAlreadyRunning)
	got := MapError(err)
	if got.Code != CodeAlreadyRunning {
		t.Errorf("code = %q, want %q", got.Code, CodeAlreadyRunning)
	}
}

func TestUsageErrorNotRoutedThroughMapError(t *testing.T) {
	got := UsageError(`unknown subcommand "frobnicate"`)
	if got.Code != CodeUsage {
		t.Errorf("code = %q, want %q", got.Code, CodeUsage)
	}
	if got.Message != `unknown subcommand "frobnicate"` {
		t.Errorf("message = %q", got.Message)
	}
}

func TestInternalErrorForPanicRecovery(t *testing.T) {
	got := InternalError(fmt.Sprintf("panic: %v", "boom"))
	if got.Code != CodeInternal {
		t.Errorf("code = %q, want %q", got.Code, CodeInternal)
	}
}

func TestErrorInfoWithSubject(t *testing.T) {
	got := UsageError("ignored").WithSubject("os", "field")
	if got.Subject != "os" || got.Kind != "field" {
		t.Errorf("got %+v", got)
	}
}

// Every code in codeTable is declared by Codes(). A code that reaches a
// consumer but is absent from the published list is a code no consumer can
// generate a switch for.
func TestCodesCoversTheTable(t *testing.T) {
	declared := make(map[Code]bool, len(Codes()))
	for _, c := range Codes() {
		declared[c] = true
	}
	for _, row := range codeTable {
		if !declared[row.code] {
			t.Errorf("codeTable has %q, Codes() does not", row.code)
		}
	}
	for _, c := range []Code{CodeUsage, CodeInternal} {
		if !declared[c] {
			t.Errorf("Codes() is missing the CLI-owned code %q", c)
		}
	}
}

// Codes are the machine-readable half of the contract: snake_case, unique,
// and sorted, so a generated switch is stable between builds.
func TestCodesAreSnakeCaseUniqueAndSorted(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	seen := make(map[Code]bool)
	codes := Codes()
	for _, c := range codes {
		if !valid.MatchString(string(c)) {
			t.Errorf("code %q is not snake_case", c)
		}
		if seen[c] {
			t.Errorf("code %q is declared twice", c)
		}
		seen[c] = true
	}
	if !slices.IsSorted(codes) {
		t.Errorf("Codes() = %v, want sorted", codes)
	}
}

// Each qemu sentinel has exactly one row in codeTable. A sentinel with no row
// reaches a consumer as "internal" with prose, which is the shape this whole
// table exists to remove.
func TestEveryQemuSentinelHasOneRow(t *testing.T) {
	want := map[error]Code{
		qemu.ErrBinaryMissing:      CodeQemuMissing,
		qemu.ErrKVMUnusable:        CodeKVMUnusable,
		qemu.ErrStartFailed:        CodeQemuStartFailed,
		qemu.ErrMonitorUnreachable: CodeMonitorUnreachable,
		qemu.ErrMonitorRejected:    CodeMonitorRejected,
		qemu.ErrNoConsolePassword:  CodeNoConsolePassword,
		qemu.ErrShareInvalid:       CodeShareInvalid,
		qemu.ErrNoXattr:            CodeNoXattr,
		// No new code: core.ErrAlreadyRunning already names this condition,
		// and qemu cannot import core to reuse the sentinel itself.
		qemu.ErrAlreadyRunning: CodeAlreadyRunning,
	}
	for sentinel, code := range want {
		got := MapError(fmt.Errorf("%w: subject", sentinel))
		if got.Code != code {
			t.Errorf("MapError(%v).Code = %q, want %q", sentinel, got.Code, code)
		}
	}
}

// Each iso sentinel has exactly one row in codeTable.
func TestEveryISOSentinelHasOneRow(t *testing.T) {
	want := map[error]Code{
		iso.ErrDownloadFailed:   CodeDownloadFailed,
		iso.ErrDownloadStalled:  CodeDownloadStalled,
		iso.ErrChecksumMismatch: CodeChecksumMismatch,
		iso.ErrNoSuchImage:      CodeNoSuchImage,
	}
	for sentinel, code := range want {
		got := MapError(fmt.Errorf("%w: subject", sentinel))
		if got.Code != code {
			t.Errorf("MapError(%v).Code = %q, want %q", sentinel, got.Code, code)
		}
	}
}
