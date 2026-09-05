package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// A flag that was not given must leave its field alone. A flag given as the
// zero value must set it to that zero value. Comparing against the zero
// value cannot tell those apart; Parse walks fs.Visit instead. These tests
// pin that against the regression "update --ram silently cleared my share".
func TestParseUpdateOnlySetsFlagsThatWereGiven(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--ram", "8192"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.RAM == nil || *a.Patch.RAM != 8192 {
		t.Errorf("RAM = %v, want 8192", a.Patch.RAM)
	}
	for name, got := range map[string]any{
		"CPUs":    a.Patch.CPUs,
		"Share":   a.Patch.Share,
		"Disk":    a.Patch.Disk,
		"SSHPort": a.Patch.SSHPort,
		"Recipes": a.Patch.Recipes,
	} {
		if !reflect.ValueOf(got).IsNil() {
			t.Errorf("%s = %v, want nil: a flag nobody passed must stay untouched", name, got)
		}
	}
	if want := []string{"ram"}; !reflect.DeepEqual(a.Changed, want) {
		t.Errorf("Changed = %v, want %v", a.Changed, want)
	}
}

// An explicitly empty --share means "clear the share", and is the case a
// zero-value comparison gets exactly backwards.
func TestParseUpdateEmptyValueIsSetNotAbsent(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--share", ""})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.Share == nil {
		t.Fatal("Share = nil, want a pointer to the empty string")
	}
	if *a.Patch.Share != "" {
		t.Errorf("Share = %q, want empty", *a.Patch.Share)
	}
}

// A cleared recipe list must be a non-nil empty slice: core.Patch documents
// nil as "leave the list alone", so nil here would silently do nothing.
func TestParseUpdateClearingRecipesIsNonNilEmpty(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--recipes", ""})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.Recipes == nil {
		t.Fatal("Recipes = nil, want a pointer to an empty slice")
	}
	if len(*a.Patch.Recipes) != 0 {
		t.Errorf("Recipes = %v, want empty", *a.Patch.Recipes)
	}
}

func TestParseUpdateChangedNamesAreWireNames(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--ssh-port", "2299", "--cpus", "8"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(a.Changed, ",")
	// fs.Visit walks in lexical order, so the pair is deterministic.
	if joined != "cpus,ssh_port" {
		t.Errorf("Changed = %q, want %q", joined, "cpus,ssh_port")
	}
}

func TestParseUpdateNeedsAtLeastOneFlag(t *testing.T) {
	if _, err := Parse([]string{"update", "work"}); err == nil {
		t.Error("update with no flags was accepted; it would be a no-op write")
	}
	if _, err := Parse([]string{"update"}); err == nil {
		t.Error("update with no vm name was accepted")
	}
}

func TestParseUpdateRecipeContractFlagsMatchE2EInvocation(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--recipes", "xfce,docker,redaction", "--set", "docker.user=dev", "--secret", "redaction.token"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.Recipes == nil || !reflect.DeepEqual(*a.Patch.Recipes, []string{"xfce", "docker", "redaction"}) {
		t.Fatalf("Recipes = %v, want comma-separated three-recipe list", a.Patch.Recipes)
	}
	if len(a.Params) != 2 || a.Params[0].Recipe != "docker" || a.Params[0].Param != "user" || a.Params[1].Recipe != "redaction" || !a.Params[1].Secret {
		t.Fatalf("Params = %+v, want non-secret set plus secret target", a.Params)
	}
}

// Keep every command shape used by scripts/e2e.sh reachable through the
// public parser. This catches a stale script flag before a real VM run; it
// deliberately stops at Parse and never starts QEMU or executes a guest.
func TestParseE2ECommandShapes(t *testing.T) {
	commands := [][]string{
		{"create", "e2e", "--image", "alpine", "--mode", "disk", "--ram", "2048", "--cpus", "2", "--recipes", "xfce"},
		{"up", "e2e"},
		{"exec", "e2e", "--", "sh", "-c", "test -s /mnt/work/.installed"},
		{"update", "e2e", "--recipes", "xfce,docker,redaction", "--set", "docker.user=dev", "--secret", "redaction.token"},
		{"apply", "e2e"},
		{"wait", "e2e", "--healthy", "--timeout", "90s"},
		{"get", "e2e"},
		{"update", "e2e", "--set", "docker.user=e2e-rerun"},
		{"apply", "e2e", "--dry-run"},
		{"down", "e2e"},
		{"rm", "e2e", "-y"},
	}
	for _, argv := range commands {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			if _, err := Parse(argv); err != nil {
				t.Fatalf("Parse(%q) = %v", argv, err)
			}
		})
	}
}

// End to end against a real data root: the field asked for changes, and the
// one that was never mentioned survives. This is the regression that a
// Parse-only test cannot prove, since it is core.Update that does the write.
func TestUpdateLeavesUnmentionedFieldsAlone(t *testing.T) {
	cliRoot(t)
	v := &config.VM{
		Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200,
		Share: "/home/someone/src", Recipes: []string{"xfce"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := Main([]string{"--json", "update", "work", "--ram", "8192"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}

	got, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.RAM != 8192 {
		t.Errorf("RAM = %d, want 8192", got.RAM)
	}
	if got.Share != "/home/someone/src" {
		t.Errorf("Share = %q, want it untouched", got.Share)
	}
	if !reflect.DeepEqual(got.Recipes, []string{"xfce"}) {
		t.Errorf("Recipes = %v, want them untouched", got.Recipes)
	}
	if got.CPUs != 1 {
		t.Errorf("CPUs = %d, want 1", got.CPUs)
	}
}

func TestUpdateClearsAShareWhenAskedTo(t *testing.T) {
	cliRoot(t)
	v := &config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200, Share: "/home/someone/src"}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := Main([]string{"--json", "update", "work", "--share", ""}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s%s", code, out.String(), errOut.String())
	}

	got, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Share != "" {
		t.Errorf("Share = %q, want it cleared", got.Share)
	}
}

func TestUpdateJSONReportsChangedAndAppliesAt(t *testing.T) {
	cliRoot(t)
	if err := (&config.VM{Name: "work", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	_, objs := runJSON(t, "update", "work", "--ram", "2048")
	data, _ := result(t, objs)["data"].(map[string]any)

	changed, _ := data["changed"].([]any)
	if len(changed) != 1 || changed[0] != "ram" {
		t.Errorf("changed = %v, want [ram]", data["changed"])
	}
	// The VM is stopped, so the write is live immediately.
	if data["applies_at"] != "now" {
		t.Errorf("applies_at = %v, want now", data["applies_at"])
	}
	if _, ok := data["vm"].(map[string]any); !ok {
		t.Errorf("vm = %#v, want the VM object", data["vm"])
	}
}
