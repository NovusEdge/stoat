package mcpsrv

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/core"
)

func TestWaitClampsTimeout(t *testing.T) {
	for _, c := range []struct {
		in   int
		want time.Duration
	}{
		{0, defaultWaitSecs * time.Second},
		{1, time.Second},
		{120, 120 * time.Second},
		{9000, maxWaitSecs * time.Second},
	} {
		if got := waitTimeout(c.in); got != c.want {
			t.Errorf("waitTimeout(%d) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestForwardRefusesFlagPair(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "forward", map[string]any{"vm": "work", "pairs": []string{"--clear"}})
	if !res.IsError {
		t.Fatal("forward accepted a pair kong reads as a flag")
	}
}

func TestCreateTakesCatalogImageIDOnly(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	res := callTool(t, "create", map[string]any{"name": "work", "image": "/home/me/my.qcow2"})
	if !res.IsError {
		t.Fatal("create accepted a bring-your-own image path")
	}
	raw, _ := json.Marshal(res.Content)
	if !strings.Contains(string(raw), "catalog image ids") {
		t.Fatalf("refusal did not name the rule: %s", raw)
	}
}

func TestUpdateRefusesAnUnknownRecipe(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	res := callTool(t, "update", map[string]any{"vm": "dev", "recipes": []string{"no-such-recipe"}})
	if !res.IsError {
		t.Fatal("update accepted a recipe that does not exist")
	}
	if meta, _ := decodeErrorContract(t, res); meta.Code != "not_found" {
		t.Fatalf("code = %q, want not_found", meta.Code)
	}
}

func TestPendingRestart(t *testing.T) {
	before := core.VM{State: core.StateRunning, RAM: 2048, CPUs: 2, SSHPort: 2201}
	after := core.VM{RAM: 3072, CPUs: 2, SSHPort: 2202}
	got := pendingRestart(before, after)
	if len(got) != 2 || got[0] != "ram_mb" || got[1] != "ssh_port" {
		t.Fatalf("pendingRestart = %v, want ram_mb and ssh_port", got)
	}
	before.State = core.StateStopped
	if got := pendingRestart(before, after); got != nil {
		t.Fatalf("a stopped VM has nothing pending, got %v", got)
	}
}

func TestDestroyReturnsALeanResult(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	res := callTool(t, "destroy", map[string]any{"vm": "dev"})
	if res.IsError {
		t.Fatalf("destroy failed: %+v", res.Content)
	}
	sameJSON(t, res.StructuredContent, `{"name":"dev","destroyed":true}`)
}

func TestCreateDescriptionStatesTheDefaults(t *testing.T) {
	for _, want := range []string{
		fmt.Sprint(core.DefaultRAM), fmt.Sprint(core.DefaultCPUs), core.DefaultDisk, "cloud",
	} {
		if !strings.Contains(createDescription, want) {
			t.Errorf("create description does not mention %q", want)
		}
	}
}

func TestWaitResultOmitsHealthyUnlessAsked(t *testing.T) {
	raw, _ := json.Marshal(wire.WaitResult{VM: "dev", Until: "reachable"})
	if strings.Contains(string(raw), "healthy") {
		t.Fatalf("a reachable wait reports health: %s", raw)
	}
	raw, _ = json.Marshal(wire.WaitResult{VM: "dev", Until: "healthy", Healthy: true})
	if !strings.Contains(string(raw), `"healthy":true`) {
		t.Fatalf("a healthy wait does not report it: %s", raw)
	}
}

func TestUpdateStripsForbiddenKeys(t *testing.T) {
	// update's own input struct has no share field, and the patch it builds
	// still goes through stripForbidden: an agent that reads a VM back and
	// passes it here must find the field inert, not effective.
	p := patchFromUpdate(updateIn{VM: "work", RAMMB: 2048})
	if _, ok := p["share"]; ok {
		t.Fatal("share reached the patch")
	}
	if p["ram"] != 2048 {
		t.Fatalf("ram = %v", p["ram"])
	}
}
