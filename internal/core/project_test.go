package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/project"
	"github.com/novusedge/stoat/internal/recipes"
)

// projectDir writes a stoat.toml into a fresh directory, chdirs there and
// returns the loaded project. Every project test runs from inside the project
// directory, the same place a user runs stoat from. It also points
// STOAT_HOME at a fresh data root with alpine-virt marked downloaded and the
// bundled recipes installed, since every declaration in this file resolves
// that image and most declare the docker recipe.
func projectDir(t *testing.T, body string) *project.Project {
	t.Helper()
	home := root(t)
	haveImage(t, home, "alpine-virt-3.24.1-x86_64.iso")
	if err := recipes.Install(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, project.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	p, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const fullDecl = `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`

func TestSpecForFullDeclaration(t *testing.T) {
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "myrepo-dev" {
		t.Errorf("name = %q, want myrepo-dev", s.Name)
	}
	if s.Image != "alpine-virt" || s.CPUs != 2 || s.RAM != 2048 {
		t.Errorf("spec = %+v", s)
	}
	if s.Project != p.Dir {
		t.Errorf("project = %q, want %q", s.Project, p.Dir)
	}
	if len(s.Shares) != 1 || s.Shares[0].Guest != "/work" {
		t.Errorf("shares = %+v", s.Shares)
	}
	if s.Params["docker"]["user"] != "dev" {
		t.Errorf("params = %v", s.Params)
	}
	if s.AgentAccess != "observe" {
		t.Errorf("agent_access = %q, want observe", s.AgentAccess)
	}
}

// A minimal declaration takes stoat new's defaults for everything but image.
func TestSpecForMinimalDeclaration(t *testing.T) {
	p := projectDir(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.ci]\nimage = \"alpine-virt\"\n")
	s, err := SpecFor(p, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "myrepo-ci" || s.Image != "alpine-virt" {
		t.Errorf("spec = %+v", s)
	}
	if s.RAM != 0 || s.CPUs != 0 || s.Disk != "" {
		t.Errorf("spec sets a default it should leave to plan(): %+v", s)
	}
	if len(s.Shares) != 0 {
		t.Errorf("shares = %+v, want none", s.Shares)
	}
}

// The vm.toml a declaration produces is the golden this whole feature rests
// on: a contributor's stoat up must build the same VM the author's did.
func TestSpecForWritesTheExpectedVMToml(t *testing.T) {
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "myrepo-dev" || v.RAM != 2048 || v.CPUs != 2 {
		t.Errorf("vm = %+v", v)
	}
	if v.Project != p.Dir {
		t.Errorf("project = %q, want %q", v.Project, p.Dir)
	}
	if len(v.Shares) != 1 || v.Shares[0].Tag != "p0" || v.Shares[0].Guest != "/work" {
		t.Errorf("shares = %+v", v.Shares)
	}
}

func TestDiffReportsEveryMutableField(t *testing.T) {
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(s); err != nil {
		t.Fatal(err)
	}

	// Re-declare with every mutable field changed.
	changed := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 4
ram     = 4096
recipes = ["docker", "devtools"]
agent_access = "exec"

[vms.dev.params.docker]
user = "build"
`
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	drift, err := Diff(p2, "dev")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{ // field -> needs restart
		"cpus": true, "ram": true, "shares": true,
		"recipes": false, "params": false, "agent_access": false,
	}
	got := map[string]bool{}
	for _, d := range drift {
		if d.Key != "dev" {
			t.Errorf("drift key = %q, want dev", d.Key)
		}
		got[d.Field] = d.NeedsRestart
	}
	for f, restart := range want {
		r, ok := got[f]
		if !ok {
			t.Errorf("no drift reported for %s: %+v", f, drift)
			continue
		}
		if r != restart {
			t.Errorf("%s needs_restart = %v, want %v", f, r, restart)
		}
	}
	for _, d := range drift {
		if d.Field == "cpus" && (d.From != "2" || d.To != "4") {
			t.Errorf("cpus drift = %+v, want 2 -> 4", d)
		}
	}
}

func TestDiffOnAnUnchangedDeclarationIsEmpty(t *testing.T) {
	p := projectDir(t, fullDecl)
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(s); err != nil {
		t.Fatal(err)
	}
	drift, err := Diff(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Errorf("drift = %+v, want none", drift)
	}
}

func TestDiffRefusesAnImageChange(t *testing.T) {
	p := projectDir(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"alpine-virt\"\n")
	haveImage(t, os.Getenv("STOAT_HOME"), "alpine-standard-3.24.1-x86_64.iso")
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName),
		[]byte("schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"alpine-standard\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Diff(p2, "dev")
	if !errors.Is(err, ErrImmutableDeclaration) {
		t.Fatalf("err = %v, want ErrImmutableDeclaration", err)
	}
	if !strings.Contains(err.Error(), "dev: image changed (alpine-virt -> alpine-standard)") ||
		!strings.Contains(err.Error(), "stoat rm dev") {
		t.Errorf("err = %q, want the image-changed message with the stoat rm hint", err.Error())
	}
}

func TestDiffRefusesADiskChange(t *testing.T) {
	p := projectDir(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"debian-13\"\ndisk  = \"10G\"\n")
	haveImage(t, os.Getenv("STOAT_HOME"), "debian-13-genericcloud-amd64.qcow2")
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(s); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName),
		[]byte("schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"debian-13\"\ndisk  = \"20G\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Diff(p2, "dev")
	if !errors.Is(err, ErrImmutableDeclaration) {
		t.Fatalf("err = %v, want ErrImmutableDeclaration", err)
	}
	if !strings.Contains(err.Error(), "dev: disk changed (10G -> 20G)") ||
		!strings.Contains(err.Error(), "stoat rm dev") {
		t.Errorf("err = %q, want the disk-changed message with the stoat rm hint", err.Error())
	}
}

// A declaration that omits disk takes stoat new's default, the same as a
// minimal declaration does for every other field. vm.toml then records that
// default rather than an empty string, and re-declaring the same omission
// must not read as the user having changed disk.
func TestDiffOmittedDiskMatchesTheDefault(t *testing.T) {
	p := projectDir(t, "schema = 1\n\n[project]\nname = \"myrepo\"\n\n[vms.dev]\nimage = \"debian-13\"\n")
	haveImage(t, os.Getenv("STOAT_HOME"), "debian-13-genericcloud-amd64.qcow2")
	s, err := SpecFor(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(s); err != nil {
		t.Fatal(err)
	}
	drift, err := Diff(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drift {
		if d.Field == "disk" {
			t.Errorf("disk reported as drift for an omitted field: %+v", d)
		}
	}
}

func TestReconcileCreatesAMissingVM(t *testing.T) {
	p := projectDir(t, fullDecl)
	r, err := Reconcile(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Created || r.Name != "myrepo-dev" || r.Key != "dev" {
		t.Fatalf("reconciled = %+v", r)
	}
	if _, err := Get("myrepo-dev"); err != nil {
		t.Fatalf("vm was not created: %v", err)
	}
}

func TestReconcileAppliesDriftAndReportsRestart(t *testing.T) {
	p := projectDir(t, fullDecl)
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName), []byte(`
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 8
ram     = 2048
recipes = ["docker"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Reconcile(p2, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Created {
		t.Error("Created = true on an existing VM")
	}
	if !r.RestartPending {
		t.Errorf("RestartPending = false after a cpus change: %+v", r.Drift)
	}
	v, err := Get("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	if v.CPUs != 8 {
		t.Errorf("cpus = %d, want 8; the drift was reported but not applied", v.CPUs)
	}
}

// writeProjectSecrets writes a project's .stoat/secrets.toml, 0600 as
// project.Secrets requires.
func writeProjectSecrets(t *testing.T, dir, body string) {
	t.Helper()
	cache := filepath.Join(dir, project.CacheDir)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "secrets.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileDropsOneParamButKeepsTheRecipe(t *testing.T) {
	p := projectDir(t, fullDecl)
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	// Same recipe, no [vms.dev.params.docker] table: the declaration no
	// longer states a user, so vm.toml must stop holding one.
	dropped := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker"]
shares  = ["."]
agent_access = "observe"
`
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName), []byte(dropped), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p2, "dev"); err != nil {
		t.Fatal(err)
	}

	v, err := load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Params["docker"]["user"]; ok {
		t.Errorf("params = %+v, want docker.user unset", v.Params)
	}

	drift, err := Diff(p2, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Errorf("second diff = %+v, want none", drift)
	}
}

func TestReconcileDropsAWholeRecipeWithParams(t *testing.T) {
	p := projectDir(t, fullDecl)
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	// docker is gone from recipes and its params table is gone with it.
	dropped := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = []
shares  = ["."]
agent_access = "observe"
`
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName), []byte(dropped), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p2, "dev"); err != nil {
		t.Fatal(err)
	}

	v, err := load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range v.Recipes {
		if r == "docker" {
			t.Errorf("recipes = %v, want docker dropped", v.Recipes)
		}
	}
	if _, ok := v.Params["docker"]; ok {
		t.Errorf("params = %+v, want the docker table dropped", v.Params)
	}

	drift, err := Diff(p2, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Errorf("second diff = %+v, want none", drift)
	}
}

// A recipe with a secret, dropped from the declaration, must take that
// secret with it the same way TestReconcileDropsAWholeRecipeWithParams
// shows a non-secret param going.
func TestReconcileDropsARecipeAndItsSecret(t *testing.T) {
	withTailscale := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker", "tailscale"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`
	p := projectDir(t, withTailscale)
	writeProjectSecrets(t, p.Dir, "[dev.tailscale]\nauthkey = \"first-key\"\n")
	p, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	// tailscale is gone from recipes; its secret goes with it.
	dropped := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`
	if err := os.WriteFile(filepath.Join(p.Dir, project.FileName), []byte(dropped), 0o644); err != nil {
		t.Fatal(err)
	}
	p2, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p2, "dev"); err != nil {
		t.Fatal(err)
	}

	v, err := load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets["tailscale"]; ok {
		t.Errorf("secrets = %+v, want tailscale dropped", secrets)
	}

	drift, err := Diff(p2, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Errorf("second diff = %+v, want none", drift)
	}
}

// A repeat Reconcile of an unchanged declaration must not rewrite the VM's
// secrets.toml: commitUpdate replaces the file by rename, so an unnecessary
// write still changes its mtime and inode even when the content is the same.
func TestReconcileWithNoDriftDoesNotRewriteSecrets(t *testing.T) {
	withTailscale := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker", "tailscale"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`
	p := projectDir(t, withTailscale)
	writeProjectSecrets(t, p.Dir, "[dev.tailscale]\nauthkey = \"first-key\"\n")
	p, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	v, err := load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	secretsPath := filepath.Join(v.Dir, config.SecretsName)
	before, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	after, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		t.Errorf("secrets.toml was rewritten by a no-op reconcile: mtime %v -> %v", before.ModTime(), after.ModTime())
	}
}

func TestReconcileAppliesASecretsOnlyChange(t *testing.T) {
	withTailscale := `
schema = 1

[project]
name = "myrepo"

[vms.dev]
image   = "alpine-virt"
cpus    = 2
ram     = 2048
recipes = ["docker", "tailscale"]
shares  = ["."]
agent_access = "observe"

[vms.dev.params.docker]
user = "dev"
`
	p := projectDir(t, withTailscale)
	writeProjectSecrets(t, p.Dir, "[dev.tailscale]\nauthkey = \"first-key\"\n")
	p, err := project.Load(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}

	writeProjectSecrets(t, p.Dir, "[dev.tailscale]\nauthkey = \"second-key\"\n")
	drift, err := Diff(p, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 {
		t.Errorf("drift = %+v, want none; this is a secrets-only change", drift)
	}

	if _, err := Reconcile(p, "dev"); err != nil {
		t.Fatal(err)
	}
	v, err := load("myrepo-dev")
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["tailscale"]["authkey"] != "second-key" {
		t.Errorf("stored authkey = %q, want second-key", secrets["tailscale"]["authkey"])
	}
}
