package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
	"github.com/novusedge/stoat/internal/provider/gce"
	"github.com/novusedge/stoat/internal/sshx"
)

// fakeGCE stands in for internal/provider/gce.Provider so this package can
// test the command's wiring without a real Compute Engine client.
type fakeGCE struct {
	newDeadline time.Time
	extendErr   error
	gotDuration time.Duration
}

func (*fakeGCE) Name() string                                      { return "gce" }
func (*fakeGCE) Capabilities(*config.VM) []capabilities.Capability { return nil }
func (*fakeGCE) Start(context.Context, *config.VM) error           { return nil }
func (*fakeGCE) Stop(context.Context, *config.VM) error            { return nil }
func (*fakeGCE) Create(context.Context, *config.VM) error          { return nil }
func (*fakeGCE) Destroy(context.Context, *config.VM) error         { return nil }
func (*fakeGCE) Status(context.Context, *config.VM) (provider.Status, error) {
	return provider.Status{Running: true}, nil
}
func (*fakeGCE) Endpoint(context.Context, *config.VM) (sshx.Endpoint, error) {
	return sshx.Endpoint{}, nil
}

func (f *fakeGCE) Extend(_ context.Context, _ *config.VM, d time.Duration) (time.Time, error) {
	f.gotDuration = d
	if f.extendErr != nil {
		return time.Time{}, f.extendErr
	}
	return f.newDeadline, nil
}

// installFakeGCE registers f under "gce" for one test, restoring the real
// gce.Provider afterward.
func installFakeGCE(t *testing.T, f provider.Provider) {
	t.Helper()
	provider.Register("gce", f)
	t.Cleanup(func() { provider.Register("gce", gce.Provider{}) })
}

func TestGCEExtendMovesTheSoftDeadline(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "cloudy", OS: "ubuntu", Provider: "gce", GCEProject: "p", GCEZone: "z", SSHPort: 2200})

	want := time.Now().Add(90 * time.Minute).UTC()
	f := &fakeGCE{newDeadline: want}
	installFakeGCE(t, f)

	var out, errOut bytes.Buffer
	code := Main([]string{"gce", "extend", "cloudy", "90m"}, "test", nil, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("gce extend: exit %d, stderr %q", code, errOut.String())
	}
	if f.gotDuration != 90*time.Minute {
		t.Errorf("Extend got duration %s, want 90m", f.gotDuration)
	}
	if !strings.Contains(out.String(), want.Format(time.RFC3339)) {
		t.Errorf("output = %q, want it to name the new deadline %s", out.String(), want)
	}
}

func TestGCEExtendJSONReportsBothDeadlines(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "cloudy", OS: "ubuntu", Provider: "gce", GCEProject: "p", GCEZone: "z", SSHPort: 2200})

	want := time.Now().Add(4 * time.Hour).UTC()
	installFakeGCE(t, &fakeGCE{newDeadline: want})

	var out, errOut bytes.Buffer
	code := Main([]string{"--json", "gce", "extend", "cloudy", "4h"}, "test", nil, &out, &errOut)
	if code != ExitOK {
		t.Fatalf("gce extend --json: exit %d, stderr %q", code, errOut.String())
	}
	var obj map[string]any
	if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
		t.Fatalf("output is not one JSON object: %v (%s)", err, out.String())
	}
	data, _ := obj["data"].(map[string]any)
	if data == nil {
		t.Fatalf("result has no data object: %s", out.String())
	}
	if _, ok := data["hard_deadline"]; !ok {
		t.Error("--json result has no hard_deadline field")
	}
	if data["soft_deadline"] != want.Format(time.RFC3339) {
		t.Errorf("soft_deadline = %v, want %s", data["soft_deadline"], want.Format(time.RFC3339))
	}
}

func TestGCEExtendRefusesOnAQemuVM(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "local", Mode: "live", OS: "alpine", RAM: 512, CPUs: 1, SSHPort: 2201})

	var out, errOut bytes.Buffer
	code := Main([]string{"gce", "extend", "local", "1h"}, "test", nil, &out, &errOut)
	if code == ExitOK {
		t.Fatal("gce extend on a qemu VM succeeded; want a refusal")
	}
	if !strings.Contains(errOut.String(), "not a gce VM") {
		t.Errorf("stderr = %q, want it to say the VM is not a gce VM", errOut.String())
	}
}

func TestGCEExtendGrammarRejectsABadDuration(t *testing.T) {
	if _, err := Parse([]string{"gce", "extend", "cloudy", "not-a-duration"}); err == nil {
		t.Fatal("Parse() = nil error for an unparseable duration")
	}
}
