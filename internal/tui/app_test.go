package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func TestPreflightReportOmitsOptionalHostFailures(t *testing.T) {
	got := preflightReport([]core.HostCheck{
		{Name: "git", Detail: "not found", Optional: true, Fix: []string{"install git"}},
		{Name: "qemu-img", Detail: "not found", Fix: []string{"install qemu-img"}},
	})
	if strings.Contains(got, "git") {
		t.Fatalf("optional git failure appeared in preflight report: %q", got)
	}
	if !strings.Contains(got, "qemu-img") || !strings.Contains(got, "install qemu-img") {
		t.Fatalf("required host failure missing from preflight report: %q", got)
	}
}
