package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

// afterStart is exercised directly rather than through runUp, which would
// need core.Start to actually launch qemu. fakeRunning marks the VM as
// running the same way apply_test.go does, so afterStart's own decision
// (core.NeedsProvision, --no-apply) is tested without a real boot or a real
// ssh wait.
func startedVM(t *testing.T, name string, patch func(*config.VM)) core.VM {
	t.Helper()
	dir := cliRoot(t)
	v := &config.VM{Name: name, Mode: "live", OS: "alpine", RAM: 512, CPUs: 1, SSHPort: 2200}
	if patch != nil {
		patch(v)
	}
	saveVM(t, v)
	v.Dir = filepath.Join(dir, name)
	t.Cleanup(fakeRunning(t, v))

	cv, err := core.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return cv
}

// TestAfterStartSkipsWhenNothingPending: a VM with no recipes and no share
// has nothing for a provision run to do, so `up` must return without
// waiting for ssh.
func TestAfterStartSkipsWhenNothingPending(t *testing.T) {
	v := startedVM(t, "work", nil)

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work"}
	code := afterStart(a, v, &out, &errOut)

	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "applying recipes") || strings.Contains(out.String(), "waiting for ssh") {
		t.Errorf("waited on a VM with nothing pending: %q", out.String())
	}
}

// TestAfterStartNoApplySkipsEvenWithRecipesPending: --no-apply must win over
// core.NeedsProvision, not just apply when there is nothing to do.
func TestAfterStartNoApplySkipsEvenWithRecipesPending(t *testing.T) {
	v := startedVM(t, "work", func(v *config.VM) {
		v.Recipes = []string{"xfce.alpine.sh"}
	})

	var out, errOut strings.Builder
	a := &Args{Cmd: "up", VM: "work", NoApply: true}
	code := afterStart(a, v, &out, &errOut)

	if code != ExitOK {
		t.Fatalf("code = %d, want ExitOK: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "applying recipes") || strings.Contains(out.String(), "waiting for ssh") {
		t.Errorf("--no-apply did not skip the apply: %q", out.String())
	}
}
