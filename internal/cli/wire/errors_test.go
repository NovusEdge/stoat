package wire

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func TestMapErrorEveryCoreSentinel(t *testing.T) {
	tests := []struct {
		err  error
		code string
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
		{fmt.Errorf("%w: work", core.ErrAppliedAtBoot), CodeAppliedAtBoot},
		{fmt.Errorf("%w: %q", core.ErrUnknownWhich, "bogus"), CodeUnknownLog},
		{context.DeadlineExceeded, CodeTimeout},
		{context.Canceled, CodeCanceled},
		{ErrConfirmationRequired, CodeConfirmationRequired},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
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
// case the design doc calls out (§2): Update's disk-grow refusal on a
// running VM wraps ErrAlreadyRunning directly (never ErrDiskShrink), so the
// wire code must be already_running, the truthful "stop it first", not a
// disk-specific code.
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
