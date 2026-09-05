package mcpsrv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// writeVM creates a VM directory whose vm.toml declares one access level.
func writeVM(t *testing.T, name, level string) {
	t.Helper()
	dir := filepath.Join(config.Root(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"" + name + "\"\nagent_access = \"" + level + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRequireAccess asserts every cell of the access table: each
// guest-touching tool against each of the four levels.
func TestRequireAccess(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	levels := []Level{LevelNone, LevelObserve, LevelManage, LevelExec}
	for _, level := range levels {
		writeVM(t, level.String(), level.String())
	}
	for _, spec := range toolTable {
		if spec.Access == LevelNone {
			continue // A host-side tool is not gated.
		}
		for _, have := range levels {
			t.Run(spec.Name+"/"+have.String(), func(t *testing.T) {
				err := requireAccess(have.String(), spec.Access)
				allowed := have.rank() >= spec.Access.rank()
				if allowed && err != nil {
					t.Fatalf("%s at %s was refused: %v", spec.Name, have, err)
				}
				if !allowed && err == nil {
					t.Fatalf("%s at %s was allowed, needs %s", spec.Name, have, spec.Access)
				}
			})
		}
	}
}

func TestRequireAccessNamesTheLevel(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	err := requireAccess("dev", LevelManage)
	if err == nil {
		t.Fatal("no refusal")
	}
	want := `vm "dev" has agent_access = observe; needs manage`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("message = %q, want it to contain %q", err, want)
	}
}

func TestParseLevel(t *testing.T) {
	for _, s := range []string{"none", "observe", "manage", "exec"} {
		if _, err := ParseLevel(s); err != nil {
			t.Errorf("ParseLevel(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "None", "root", "all"} {
		if _, err := ParseLevel(s); err == nil {
			t.Errorf("ParseLevel(%q) accepted", s)
		}
	}
}

func TestPlanRecipesNeedsNoAccess(t *testing.T) {
	// plan_recipes is computed on the host and runs nothing in the guest, so
	// it works on a VM at agent_access = none.
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "locked", "none")
	res := callTool(t, "plan_recipes", map[string]any{"vm": "locked"})
	if res.IsError {
		t.Fatalf("plan_recipes refused on a locked VM: %+v", res.Content)
	}
}

func TestUpdateRefusesToRaiseTheLevel(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	writeVM(t, "dev", "observe")
	if res := callTool(t, "update", map[string]any{"vm": "dev", "agent_access": "exec"}); !res.IsError {
		t.Fatal("update raised the access level")
	}
	if res := callTool(t, "update", map[string]any{"vm": "dev", "agent_access": "none"}); res.IsError {
		t.Fatalf("update could not lower the access level: %+v", res.Content)
	}
}
