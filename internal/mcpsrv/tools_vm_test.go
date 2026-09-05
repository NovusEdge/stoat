package mcpsrv

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWaitClampsTimeout(t *testing.T) {
	for _, c := range []struct {
		in   int
		want time.Duration
	}{
		{0, time.Second},
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
