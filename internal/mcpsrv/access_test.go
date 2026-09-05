package mcpsrv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/testutil"
)

// writeVM creates a VM directory whose vm.toml declares one access level.
// The os key names a real guest definition: pkg_install and useradd read the
// guest file for the distro's own verbs and refuse a VM whose os is unknown.
func writeVM(t *testing.T, name, level string) {
	t.Helper()
	dir := filepath.Join(config.Root(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"" + name + "\"\nos = \"alpine\"\nagent_access = \"" + level + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "vm.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSecrets writes a VM's secrets.toml at the mode the loader requires.
func writeSecrets(t *testing.T, vm string, kv map[string]string) {
	t.Helper()
	var b strings.Builder
	for k, v := range kv {
		b.WriteString(k + " = \"" + v + "\"\n")
	}
	path := filepath.Join(config.Root(), vm, "secrets.toml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// accessFakeGuest answers every guest command a tool in the table can send:
// enough exit-0 output for read_file, list_dir, stat and ps to parse
// cleanly, and a bare success for everything else. Access denial is decided
// before any of this runs, so this is only exercised at an allowed level.
const accessFakeGuest = `cat > /dev/null 2>&1
case "$1" in
  stat) echo 5;;
  *) exit 0;;
esac`

// accessArgsFor builds the minimal valid input for one table tool, so a call
// reaches the handler's own requireAccess line instead of failing input
// validation first. The content of a field that survives the access gate
// (a bogus job id, a host path outside the sandbox) does not matter: those
// calls are allowed to fail for a reason that is not access.
func accessArgsFor(tool, vm string) map[string]any {
	args := map[string]any{"vm": vm}
	switch tool {
	case "read_file", "list_dir", "stat":
		args["path"] = "/etc/hostname"
	case "svc_status":
		args["name"] = "sshd"
	case "write_file":
		args["path"], args["content"] = "/tmp/x", "x"
	case "copy_to", "copy_from":
		args["local"], args["remote"] = "/etc/passwd", "/tmp/x"
	case "pkg_install":
		args["packages"] = []string{"curl"}
	case "svc":
		args["name"], args["action"] = "sshd", "restart"
	case "useradd":
		args["name"] = "bob"
	case "exec", "exec_bg":
		args["argv"] = []string{"true"}
	case "job_status", "job_output", "job_kill":
		args["job_id"] = "j-00000000"
	}
	return args
}

// isAccessRefusal reports whether res was refused by requireAccess rather
// than by anything downstream. requireAccess's message is the only place
// "agent_access =" appears in a tool's output.
func isAccessRefusal(t *testing.T, res *mcp.CallToolResult) bool {
	t.Helper()
	raw, err := json.Marshal(res.Content)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), "agent_access =")
}

// TestRequireAccess drives every guest-touching tool through callTool, the
// path a real client takes, at each of the four levels. Calling
// requireAccess directly would only check the table's own expectation
// against itself; this instead exercises the Level literal each handler
// passes to requireAccess, so a handler gated at the wrong level fails here.
func TestRequireAccess(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	levels := []Level{LevelNone, LevelObserve, LevelManage, LevelExec}
	for _, level := range levels {
		writeVM(t, level.String(), level.String())
	}
	testutil.FakeSSH(t, accessFakeGuest)
	for _, spec := range toolTable {
		if spec.Access == LevelNone {
			continue // A host-side tool is not gated.
		}
		for _, have := range levels {
			t.Run(spec.Name+"/"+have.String(), func(t *testing.T) {
				res := callTool(t, spec.Name, accessArgsFor(spec.Name, have.String()))
				allowed := have.rank() >= spec.Access.rank()
				refused := res.IsError && isAccessRefusal(t, res)
				if allowed && refused {
					t.Fatalf("%s at %s was refused by access, needs %s", spec.Name, have, spec.Access)
				}
				if !allowed && !refused {
					t.Fatalf("%s at %s was not refused by access: %+v", spec.Name, have, res.Content)
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
