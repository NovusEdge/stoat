package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/sshx"
)

func TestUpOnARunningVMSucceeds(t *testing.T) {
	startedVM(t, "work", nil)

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work", NoWait: true}
	if code := runUp(a, &out, &errOut); code != ExitOK {
		t.Fatalf("code = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "work is already running") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestUpOnARunningVMEmitsOneResultUnderJSON(t *testing.T) {
	startedVM(t, "work", nil)

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work", JSON: true, Quiet: true, NoWait: true}
	if code := runUp(a, &out, &errOut); code != ExitOK {
		t.Fatalf("code = %d: %s", code, out.String())
	}
	var res struct {
		OK   bool `json:"ok"`
		Data struct {
			VM struct{ Name string } `json:"vm"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if res.Data.VM.Name != "work" {
		t.Errorf("result = %s", out.String())
	}
}

func TestDownOnAStoppedVMSucceeds(t *testing.T) {
	cliRoot(t)
	saveVM(t, &config.VM{Name: "work", Mode: "live", OS: "alpine", RAM: 512, CPUs: 1, SSHPort: 2200})

	var out, errOut strings.Builder
	a := &Args{Cmd: "down", VM: "work"}
	if code := runDown(a, &out, &errOut); code != ExitOK {
		t.Fatalf("code = %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "work is already stopped") {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestRMOfAMissingVMStillFails(t *testing.T) {
	cliRoot(t)

	var out, errOut strings.Builder
	a := &Args{Cmd: "rm", VM: "nope", Yes: true}
	if code := runRM(a, strings.NewReader(""), &out, &errOut); code == ExitOK {
		t.Fatal("rm of a missing VM exited 0")
	}
}

func TestFailGuestSaysTheVMIsBooting(t *testing.T) {
	var out, errOut strings.Builder
	a := &Args{Cmd: "exec", VM: "work"}
	err := sshx.NewUnreachable("work", []byte("Connection timed out during banner exchange"))
	if code := a.failGuest(&out, &errOut, err); code != ExitFail {
		t.Fatalf("code = %d", code)
	}
	if want := "stoat: exec: work is booting; run stoat wait work\n"; errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
}

func TestWaitsAfterUp(t *testing.T) {
	var pipe strings.Builder
	for _, c := range []struct {
		name string
		a    Args
		want bool
	}{
		{"piped stdout", Args{}, true},
		{"json", Args{JSON: true}, true},
		{"explicit", Args{Wait: true}, true},
		{"opt out beats a pipe", Args{NoWait: true}, false},
		{"opt out beats json", Args{NoWait: true, JSON: true}, false},
	} {
		if got := c.a.waitsAfterUp(&pipe); got != c.want {
			t.Errorf("%s: waitsAfterUp = %v, want %v", c.name, got, c.want)
		}
	}
}
