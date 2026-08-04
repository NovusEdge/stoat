package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/cli/wire"
)

// TestKongFlagNamingSurface pins every multi-word field in grammar.go against
// the flag or command spelling kong actually derives, not the one a person
// reading camelCase would guess. Kong reads CPUs as "CP"+"Us" and would
// otherwise emit --cp-us, which is why createCmd.CPUs and updateCmd.CPUs
// carry an explicit name:"cpus" tag; if that tag is ever deleted, --cpus
// starts failing here instead of silently renaming itself in the field.
func TestKongFlagNamingSurface(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		// create's --cpus tag: without it kong derives --cp-us.
		{"create --cpus accepted", []string{"create", "n", "--image", "x", "--cpus", "4"}, false},
		{"create --cp-us rejected", []string{"create", "n", "--image", "x", "--cp-us", "4"}, true},
		// update's --cpus tag: same trap, pointer field this time.
		{"update --cpus accepted", []string{"update", "work", "--cpus", "4"}, false},
		{"update --cp-us rejected", []string{"update", "work", "--cp-us", "4"}, true},
		// update's --ssh-port tag: SSH is all-caps, Port is not; pinned so a
		// dropped tag surfaces as a failing test rather than a silent --ssh-port
		// still working by accident of the same derivation.
		{"update --ssh-port accepted", []string{"update", "work", "--ssh-port", "2201"}, false},
		{"update --sshport rejected", []string{"update", "work", "--sshport", "2201"}, true},
		// create's ConsolePassword has NO explicit name tag: kong's default
		// derivation already lands on --console-password, so this pins that the
		// untagged case keeps working, not that it needs a tag.
		{"create --console-password accepted (no tag needed)", []string{"create", "n", "--image", "x", "--console-password", "random"}, false},
		// Command names. SSHCmd carries an explicit name:"ssh-command" tag.
		{"ssh-command accepted", []string{"ssh-command", "work"}, false},
		{"sshcmd rejected", []string{"sshcmd", "work"}, true},
		// CheckRecipes has NO name tag: kong's default derivation already lands
		// on check-recipes ("Check"+"Recipes"), pinned the same way as
		// ConsolePassword above.
		{"check-recipes accepted (no tag needed)", []string{"check-recipes", "a.sh", "--os", "alpine"}, false},
		{"checkrecipes rejected", []string{"checkrecipes", "a.sh", "--os", "alpine"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.args)
			if c.wantErr && err == nil {
				t.Errorf("Parse(%v) accepted; want a usage error", c.args)
			}
			if !c.wantErr && err != nil {
				t.Errorf("Parse(%v) errored: %v", c.args, err)
			}
		})
	}
}

// TestKongFlagNamingSurfacePopulatesTheRightField goes one step further than
// acceptance: --cpus must land on the CPUs field, not merely parse without
// error. A wrong name tag (e.g. pointing --cpus at RAM) would pass the
// acceptance test above and still be a bug.
func TestKongFlagNamingSurfacePopulatesTheRightField(t *testing.T) {
	a, err := Parse([]string{"create", "n", "--image", "x", "--cpus", "4"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Spec.CPUs != 4 {
		t.Errorf("create --cpus 4: Spec.CPUs = %d, want 4", a.Spec.CPUs)
	}

	a, err = Parse([]string{"update", "work", "--cpus", "4"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.CPUs == nil || *a.Patch.CPUs != 4 {
		t.Errorf("update --cpus 4: Patch.CPUs = %v, want *4", a.Patch.CPUs)
	}
}

// TestTrimListAcrossCommands pins trimList against every command that takes a
// comma list. Kong splits "a.sh, b.sh" on the comma but keeps the
// whitespace, so the second element would arrive as " b.sh" and fail a
// filename comparison downstream; a trailing comma additionally produces an
// empty element that must not survive as a bogus recipe/file name.
func TestTrimListAcrossCommands(t *testing.T) {
	want := []string{"a.sh", "b.sh"}

	a, err := Parse([]string{"apply", "work", "--only", "a.sh, b.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Only, want) {
		t.Errorf("apply --only = %v, want %v", a.Only, want)
	}

	// Trailing comma: kong's split leaves a third, empty element.
	a, err = Parse([]string{"apply", "work", "--only", "a.sh, b.sh,"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Only, want) {
		t.Errorf("apply --only with trailing comma = %v, want %v", a.Only, want)
	}

	// check-recipes' Names is a positional []string (arg:""), and kong's Sep
	// tag only splits the value of a FLAG, never a positional. So kong alone
	// hands "a.sh,b.sh" back as ONE element containing a comma, which the
	// stdlib parser this replaced did split, and which usage() documented as
	// `check-recipes a,b`. trimList splits positionals itself for that
	// reason: without it, two recipe names silently became one bogus name and
	// the command reported "no such recipe" for something nobody typed.
	for _, argv := range [][]string{
		{"check-recipes", "a.sh,b.sh", "--os", "alpine"},
		{"check-recipes", "a.sh, b.sh", "--os", "alpine"},
		{"check-recipes", "  a.sh  ", "b.sh", "--os", "alpine"},
		{"check-recipes", "a.sh", "b.sh", "--os", "alpine"},
	} {
		a, err = Parse(argv)
		if err != nil {
			t.Fatalf("Parse(%v): %v", argv, err)
		}
		if !reflect.DeepEqual(a.Only, want) {
			t.Errorf("Parse(%v) names = %v, want %v", argv, a.Only, want)
		}
	}

	a, err = Parse([]string{"create", "n", "--image", "x", "--recipes", "a.sh, b.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Spec.Recipes, want) {
		t.Errorf("create --recipes = %v, want %v", a.Spec.Recipes, want)
	}
}

// TestParseUpdateDiskAndSSHPortPointersAreIndependent extends
// update_test.go's pointer coverage (which only ever sets one field at a
// time) to two pointer fields at once, and to the two fields that test file
// never touches: Disk and SSHPort. Each pointer must carry its own value and
// every untouched pointer must stay nil, proving kong's per-field pointer
// wiring isn't accidentally sharing state between flags.
func TestParseUpdateDiskAndSSHPortPointersAreIndependent(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--disk", "32G", "--ssh-port", "2299"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.Disk == nil || *a.Patch.Disk != "32G" {
		t.Errorf("Disk = %v, want *32G", a.Patch.Disk)
	}
	if a.Patch.SSHPort == nil || *a.Patch.SSHPort != 2299 {
		t.Errorf("SSHPort = %v, want *2299", a.Patch.SSHPort)
	}
	for name, got := range map[string]any{"RAM": a.Patch.RAM, "CPUs": a.Patch.CPUs, "Share": a.Patch.Share, "Recipes": a.Patch.Recipes} {
		if !reflect.ValueOf(got).IsNil() {
			t.Errorf("%s = %v, want nil: flags not passed must stay untouched", name, got)
		}
	}
}

// TestParseUpdateRecipesPointerCarriesTrimmedList covers the non-empty case
// of update's --recipes pointer: update_test.go only exercises the "clear"
// case (--recipes ""). A non-empty value must reach the pointee AFTER
// trimList runs, not before, or a value like "a.sh, b.sh" would leave the
// leading space on the second entry.
func TestParseUpdateRecipesPointerCarriesTrimmedList(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--recipes", "a.sh, b.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Patch.Recipes == nil {
		t.Fatal("Recipes = nil, want a non-nil pointer")
	}
	if want := []string{"a.sh", "b.sh"}; !reflect.DeepEqual(*a.Patch.Recipes, want) {
		t.Errorf("*Recipes = %v, want %v", *a.Patch.Recipes, want)
	}
}

// TestParseExecBypassesKongForShorthandClusters pins that `exec` never
// reaches kong at all. Kong splits a leading shorthand cluster into
// individual runes, so `exec work -la` would reach the guest as "-l" "a"
// (two elements) instead of "-la" (one) if parseExec's early interception in
// Parse were ever removed.
func TestParseExecBypassesKongForShorthandClusters(t *testing.T) {
	a, err := Parse([]string{"exec", "work", "-la"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-la"}; !reflect.DeepEqual(a.Command, want) {
		t.Errorf("Command = %v, want %v (one element, not split into -l and a)", a.Command, want)
	}
}

// TestParseExecBypassesKongForLeadingQuiet pins the other half of the same
// bug: kong resolves a leading flag token against the ROOT's own flags
// before exec's passthrough positional starts consuming, so `exec work -q`
// would be swallowed as stoat's own --quiet and never reach the guest.
func TestParseExecBypassesKongForLeadingQuiet(t *testing.T) {
	a, err := Parse([]string{"exec", "work", "--quiet"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--quiet"}; !reflect.DeepEqual(a.Command, want) {
		t.Errorf("Command = %v, want %v", a.Command, want)
	}
	if a.Quiet {
		t.Error("Quiet = true; --quiet was meant for the guest, not stoat")
	}
}

// TestExecJSONFlagFollowsWireContractNotKong pins wire.SplitJSONFlag's
// documented boundary for exec: --json is stoat's own only up through exec's
// VM name (argv[0], argv[1]); anything after that, --json included, is the
// guest's command verbatim. This combines SplitJSONFlag (which Main calls
// before Parse) with Parse itself, since that's the order Main actually
// runs them in and the bug this pins is at the boundary between the two.
func TestExecJSONFlagFollowsWireContractNotKong(t *testing.T) {
	jsonMode, rest := wire.SplitJSONFlag([]string{"exec", "work", "--json", "ls"})
	if jsonMode {
		t.Fatal("--json after the vm name belongs to the guest, not stoat")
	}
	a, err := Parse(rest)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"--json", "ls"}; !reflect.DeepEqual(a.Command, want) {
		t.Errorf("Command = %v, want %v", a.Command, want)
	}
}

// TestSnapshotXor pins kong's xor:"action" tag on --restore/--delete: each
// alone (with a tag) is accepted, both together is rejected by kong itself,
// and grammar.go's own toArgs still enforces "needs a tag" for the case xor
// does not cover (kong's xor only rejects the pair, not either one alone
// without a tag).
func TestSnapshotXor(t *testing.T) {
	a, err := Parse([]string{"snapshot", "work", "tag1", "--restore"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Restore || a.Delete {
		t.Errorf("Restore=%v Delete=%v, want Restore=true Delete=false", a.Restore, a.Delete)
	}

	a, err = Parse([]string{"snapshot", "work", "tag1", "--delete"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Delete || a.Restore {
		t.Errorf("Restore=%v Delete=%v, want Restore=false Delete=true", a.Restore, a.Delete)
	}

	if _, err := Parse([]string{"snapshot", "work", "tag1", "--restore", "--delete"}); err == nil {
		t.Error("--restore and --delete together were accepted; kong's xor should reject them")
	}

	// No tag: kong's xor has nothing to say here (only one of the pair was
	// given), so this is toArgs' own check, still live after the kong move.
	if _, err := Parse([]string{"snapshot", "work", "--restore"}); err == nil {
		t.Error("--restore with no tag was accepted")
	}
}

// TestHelpListsEverySubcommand extends TestParseHelpCarriesGeneratedText's
// small spot-check to the full command surface: every leaf command kong
// generates a line for must be named here, so adding a subcommand to
// grammar.go without it appearing in the generated help fails this build
// rather than silently shipping an undocumented command.
func TestHelpListsEverySubcommand(t *testing.T) {
	want := []string{
		"ls", "get", "create", "update", "up", "down", "wait", "rm", "clone",
		"exec", "ssh", "ssh-command", "cp", "forward", "images", "pull",
		"snapshot", "prune", "apply", "provision", "recipes", "check-recipes",
		"recipe list", "recipe new", "logs", "doctor", "version", "help",
	}
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		a, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		for _, name := range want {
			if !strings.Contains(a.Help, name) {
				t.Errorf("Parse(%v) help omits subcommand %q", args, name)
			}
		}
	}
}
