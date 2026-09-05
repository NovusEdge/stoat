package mcpsrv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/core"
)

const sentinel = "tskey-auth-SENTINEL-do-not-leak"

// TestNoToolLeaksASecret scans every tool's output for a sentinel secret. The
// fixture VM carries one secret value and no tool may ever echo it.
func TestNoToolLeaksASecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	writeVM(t, "dev", "exec")
	writeSecrets(t, "dev", map[string]string{"tailscale.authkey": sentinel})

	ctx := context.Background()
	srv := New(Options{Version: "test", Limits: Limits{ToolBurst: 1000, ToolRate: 100, Burst: 10000, Rate: 100}})
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Error(err)
		}
	})

	for _, spec := range toolTable {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: spec.Name, Arguments: argsFor(spec.Name)})
		if err != nil {
			continue // A protocol failure is not a leak.
		}
		raw, _ := json.Marshal(res)
		if strings.Contains(string(raw), sentinel) {
			t.Errorf("%s leaked the secret: %s", spec.Name, raw)
		}
	}
}

func TestRedactValueReplacesSecretFields(t *testing.T) {
	in := map[string]any{
		"name":    "tailscale",
		"params":  map[string]any{"authkey": sentinel},
		"secrets": map[string]any{"authkey": sentinel},
	}
	out := redactValue(in).(map[string]any)
	if out["secrets"].(map[string]any)["authkey"] != core.SecretSet {
		t.Fatalf("a set secret must render as %q, got %v", core.SecretSet, out["secrets"])
	}
	if out["name"] != "tailscale" {
		t.Fatal("redaction changed a non-secret field")
	}
}

func TestUpdateNeverEchoesASecret(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "manage")
	res := callTool(t, "update", map[string]any{
		"vm":      "dev",
		"secrets": map[string]any{"tailscale": map[string]any{"authkey": sentinel}},
	})
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), sentinel) {
		t.Fatalf("update echoed the secret it was given: %s", raw)
	}
}

// argsFor gives every tool the minimum arguments that reach a handler, so
// the scan covers real output rather than an argument error.
func argsFor(name string) map[string]any {
	args := map[string]any{}
	switch name {
	case "create":
		return map[string]any{"name": "x", "image": "alpine-virt"}
	case "clone":
		return map[string]any{"source": "dev", "name": "dev2"}
	case "snapshot", "restore":
		return map[string]any{"vm": "dev", "tag": "t"}
	case "check_recipes":
		return map[string]any{"recipes": []string{"docker"}, "os": "alpine"}
	case "recipe_schema", "guest_info":
		return map[string]any{"name": "docker"}
	case "search_recipes":
		return map[string]any{"term": "docker"}
	case "add_recipe", "update_recipe", "remove_recipe":
		return map[string]any{"name": "docker"}
	case "read_file", "list_dir", "stat":
		return map[string]any{"vm": "dev", "path": "/etc"}
	case "write_file":
		return map[string]any{"vm": "dev", "path": "/tmp/x", "content": "x"}
	case "copy_to", "copy_from":
		return map[string]any{"vm": "dev", "local": "/tmp/x", "remote": "/tmp/x"}
	case "pkg_install":
		return map[string]any{"vm": "dev", "packages": []string{"curl"}}
	case "svc":
		return map[string]any{"vm": "dev", "name": "sshd", "action": "restart"}
	case "svc_status", "useradd":
		return map[string]any{"vm": "dev", "name": "sshd"}
	case "exec", "exec_bg":
		return map[string]any{"vm": "dev", "argv": []string{"true"}}
	case "job_status", "job_output", "job_kill":
		return map[string]any{"vm": "dev", "job_id": "j-00000001"}
	}
	if strings.Contains(name, "vm") || name == "logs" || name == "wait" ||
		name == "start" || name == "stop" || name == "destroy" || name == "update" ||
		name == "forward" || name == "ps" || name == "tail_log" ||
		name == "plan_recipes" || name == "apply_recipes" || name == "list_jobs" {
		args["vm"] = "dev"
	}
	return args
}
