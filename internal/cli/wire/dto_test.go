package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
	"github.com/novusedge/stoat/internal/guest"
)

// --- golden-file style: marshal each DTO and pin the exact JSON shape. ---

func TestVMGolden(t *testing.T) {
	got := marshal(t, FromVM(sampleVM(), true))
	want := `{"name":"work","os":"alpine","mode":"live","backend":"apkovl","state":"running","cpus":4,"ram_mb":4096,"disk":"8G","share":"/home/u/src","recipes":["xfce"],"ssh_port":2222,"ssh_user":"root","installed":false,"forwards":[{"host_port":8080,"guest_port":80}],"allow_exec":true,"display":"window","project":"","key":"","project_missing":false}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// display names the surface, never the socket. The socket is an absolute host
// path and those do not reach the wire; a rendered attach command would only
// embed the same path behind a friendlier name.
func TestVMDisplayNamesTheSurfaceNotTheSocket(t *testing.T) {
	fresh := core.VM{Name: "alpinedisk", Mode: "disk", Installed: false,
		Paths: core.Paths{VNCSocket: "/home/u/.stoat/alpinedisk/vnc.sock"}}
	got := marshal(t, FromVM(fresh, true))
	if !strings.Contains(got, `"display":"window"`) {
		t.Errorf("a VM mid-install has a real window: %s", got)
	}

	pinned := fresh
	pinned.Installed = true
	pinned.Display = "vnc"
	got = marshal(t, FromVM(pinned, true))
	if !strings.Contains(got, `"display":"vnc"`) {
		t.Errorf("a VM pinned to vnc is headless: %s", got)
	}
	if strings.Contains(got, "vnc.sock") || strings.Contains(got, "/home/u") {
		t.Errorf("the socket path reached the wire: %s", got)
	}
}

// A broken vm.toml supplies neither mode nor installed, so there is no rule
// to run and "vnc" would be a guess dressed as a fact.
func TestVMBrokenHasNoDisplay(t *testing.T) {
	got := marshal(t, FromVM(core.VM{Name: "wreck", State: core.StateBroken, Error: "bad"}, true))
	if !strings.Contains(got, `"display":""`) {
		t.Errorf("broken VM must not claim a display surface: %s", got)
	}
}

func TestVMBrokenCarriesError(t *testing.T) {
	v := core.VM{Name: "oldvm", State: core.StateBroken, Error: "broken vm.toml: oldvm: toml: line 4: ..."}
	got := marshal(t, FromVM(v, true))
	if !strings.Contains(got, `"error":"broken vm.toml: oldvm: toml: line 4: ..."`) {
		t.Errorf("expected error field in %s", got)
	}
}

// TestVMAllowExecCarriesFalse pins that the DTO does not just default the
// field to true itself; it must relay whatever core.VM says, since the MCP
// server's whole reason for reading it is to find the VMs where it's false.
func TestVMAllowExecCarriesFalse(t *testing.T) {
	v := sampleVM()
	v.AllowExec = false
	got := marshal(t, FromVM(v, true))
	if !strings.Contains(got, `"allow_exec":false`) {
		t.Errorf("expected allow_exec:false in %s", got)
	}
}

func TestImageGolden(t *testing.T) {
	img := core.CatalogImage{ID: "alpine-virt", OS: "alpine", Variant: "virt", Backend: "apkovl", File: "alpine-virt-3.24.1-x86_64.iso", Downloaded: true, Bytes: 62914560, Exact: true}
	got := marshal(t, FromCatalogImage(img))
	want := `{"id":"alpine-virt","os":"alpine","variant":"virt","backend":"apkovl","file":"alpine-virt-3.24.1-x86_64.iso","downloaded":true,"bytes":62914560,"bytes_exact":true,"byo":false}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestImageBYODerivedFromEmptyID(t *testing.T) {
	byo := FromCatalogImage(core.CatalogImage{ID: "", OS: "debian", Backend: "cloudinit", File: "my-cloud.img", Downloaded: true})
	if !byo.BYO {
		t.Error("an image with no catalog ID must be reported byo:true")
	}
	cataloged := FromCatalogImage(core.CatalogImage{ID: "alpine-virt"})
	if cataloged.BYO {
		t.Error("a catalog entry must be reported byo:false")
	}
}

func TestSnapshotGoldenUsesDisplaySuffix(t *testing.T) {
	s := core.Snapshot{Tag: "clean", VMState: true, Size: "203 MiB", Created: "2026-08-04 12:00:00"}
	got := marshal(t, FromSnapshot(s))
	want := `{"tag":"clean","vm_state":true,"size_display":"203 MiB","created_display":"2026-08-04 12:00:00"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestPruneItemGolden(t *testing.T) {
	p := core.PruneItem{Class: "orphaned_image", Path: "/home/u/.stoat/isos/old.iso"}
	got := marshal(t, FromPruneItem(p))
	want := `{"class":"orphaned_image","path":"/home/u/.stoat/isos/old.iso"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHostCheckGolden(t *testing.T) {
	c := core.HostCheck{Name: "qemu-img", OK: false, Detail: "not found", Fix: []string{"sudo", "pacman", "-S", "qemu-img"}}
	got := marshal(t, FromHostCheck(c))
	want := `{"name":"qemu-img","ok":false,"detail":"not found","fix":["sudo","pacman","-S","qemu-img"],"optional":false}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestHostCheckOptionalGolden(t *testing.T) {
	c := core.HostCheck{Name: "git", Detail: "not found", Fix: []string{"sudo", "pacman", "-S", "git"}, Optional: true}
	got := marshal(t, FromHostCheck(c))
	want := `{"name":"git","ok":false,"detail":"not found","fix":["sudo","pacman","-S","git"],"optional":true}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRecipeGolden(t *testing.T) {
	r := core.Recipe{
		Name:        "xfce",
		Description: "XFCE desktop environment",
		Reboot:      true,
		Depends:     []string{"devtools"},
		Runtime:     "sh",
	}
	got := marshal(t, FromRecipe(r))
	want := `{"name":"xfce","description":"XFCE desktop environment","schema":0,"params":[],"outputs":[],"health":null,"reboot":true,"depends":["devtools"],"runtime":"sh"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// TestRecipeNilDependsIsEmptyList pins the nil -> [] normalization this file's
// doc comment requires: a Python consumer iterating "depends" raises TypeError
// on null.
func TestRecipeNilDependsIsEmptyList(t *testing.T) {
	got := marshal(t, FromRecipe(core.Recipe{Name: "xfce"}))
	want := `{"name":"xfce","description":"","schema":0,"params":[],"outputs":[],"health":null,"reboot":false,"depends":[],"runtime":""}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestFromRecipeSchemaShape(t *testing.T) {
	got := FromRecipeSchema(core.Recipe{
		Name: "docker", Description: "Docker engine", Schema: 3, Runtime: "sh",
		Params: []core.RecipeParam{
			{Name: "channel", Type: "enum", Default: "stable", Values: []string{"stable", "test"}},
			{Name: "authkey", Type: "secret", Required: true},
		},
		Outputs: []core.RecipeOutput{{Name: "socket", Help: "path of the docker socket"}},
		Health:  &core.RecipeHealthSpec{Check: "docker info", Timeout: "30s"},
	})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"docker","description":"Docker engine","schema":3,"runtime":"sh","reboot":false,"depends":[],"params":[` +
		`{"name":"authkey","type":"secret","required":true,"default":"","values":[],"help":""},` +
		`{"name":"channel","type":"enum","required":false,"default":"stable","values":["stable","test"],"help":""}],` +
		`"outputs":[{"name":"socket","help":"path of the docker socket"}],` +
		`"health":{"check":"docker info","timeout":"30s"}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
}

// A recipe without a health check emits null, never an empty object: a
// consumer must distinguish "no check declared" from a blank check.
func TestFromRecipeSchemaNullHealth(t *testing.T) {
	b, err := json.Marshal(FromRecipeSchema(core.Recipe{Name: "xfce", Schema: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"health":null`) {
		t.Errorf("got %s", b)
	}
}

// List and show are two views of one projection. Their shared recipe fields
// must agree, including sorted, non-null parameter and output lists.
func TestRecipeListAndShowProjectionAgree(t *testing.T) {
	r := core.Recipe{
		Name: "docker", Description: "Docker", Schema: 3, Runtime: "sh",
		Depends: []string{"base"},
		Params:  []core.RecipeParam{{Name: "user", Type: "string", Default: "dev"}},
		Outputs: []core.RecipeOutput{{Name: "socket", Help: "socket"}},
		Health:  &core.RecipeHealthSpec{Check: "docker info", Timeout: "30s"},
	}
	list := FromRecipe(r)
	show := FromRecipeSchema(r)
	if list.Name != show.Name || list.Description != show.Description || list.Schema != show.Schema || list.Runtime != show.Runtime || list.Reboot != show.Reboot {
		t.Fatalf("list=%+v show=%+v disagree on shared recipe fields", list, show)
	}
	listParams, _ := json.Marshal(list.Params)
	showParams, _ := json.Marshal(show.Params)
	if string(listParams) != string(showParams) {
		t.Fatalf("list and show params disagree: list=%s show=%s", listParams, showParams)
	}
	listOutputs, _ := json.Marshal(list.Outputs)
	showOutputs, _ := json.Marshal(show.Outputs)
	if string(listOutputs) != string(showOutputs) {
		t.Fatalf("list and show outputs disagree: list=%s show=%s", listOutputs, showOutputs)
	}
	if strings.Contains(marshal(t, list), "null") || strings.Contains(marshal(t, show), "null") {
		t.Fatalf("recipe projections contain a null list: list=%s show=%s", marshal(t, list), marshal(t, show))
	}
}

func TestApplyPlanGolden(t *testing.T) {
	p := core.ApplyPlan{Name: "xfce", Action: "run", Reason: "never applied"}
	got := marshal(t, FromApplyPlan(p))
	want := `{"name":"xfce","action":"run","reason":"never applied"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRecipeIssueGolden(t *testing.T) {
	i := core.RecipeIssue{Name: "xfce", Reason: "xfce is not offered to alpine/cloudinit"}
	got := marshal(t, FromRecipeIssue(i))
	want := `{"name":"xfce","reason":"xfce is not offered to alpine/cloudinit"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// --- fields that must never reach the wire ---

func TestVMNeverSerializesConsolePassword(t *testing.T) {
	got := marshal(t, FromVM(sampleVM(), true))
	if strings.Contains(strings.ToLower(got), "password") {
		t.Fatalf("VM DTO leaked a password-shaped field: %s", got)
	}
	if strings.Contains(got, "hunter2password") {
		t.Fatalf("VM DTO leaked the actual console password value: %s", got)
	}
}

func TestVMNeverSerializesHostPaths(t *testing.T) {
	got := marshal(t, FromVM(sampleVM(), true))
	for _, p := range []string{
		"/home/u/.stoat/work",
		"disk.qcow2",
		"console.log",
		"last-provision.log",
		"vnc.sock",
		"monitor.sock",
	} {
		if strings.Contains(got, p) {
			t.Fatalf("VM DTO leaked a host path %q: %s", p, got)
		}
	}
}

func TestEmptySlicesMarshalAsEmptyArrayNeverNull(t *testing.T) {
	tests := []struct {
		name string
		got  string
	}{
		{"VM.forwards/recipes", marshal(t, FromVM(core.VM{Name: "bare"}, true))},
		{"VMs list", marshal(t, FromVMs(nil, true))},
		{"PortForwards", marshal(t, FromPortForwards(nil))},
		{"CatalogImages", marshal(t, FromCatalogImages(nil))},
		{"Snapshots", marshal(t, FromSnapshots(nil))},
		{"HostChecks", marshal(t, FromHostChecks(nil))},
		{"HostCheck.fix", marshal(t, FromHostCheck(core.HostCheck{Name: "ssh", OK: true}))},
		{"PruneItems", marshal(t, FromPruneItems(nil))},
		{"Recipes", marshal(t, FromRecipes(nil))},
		{"RecipeIssues", marshal(t, FromRecipeIssues(nil))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.Contains(tt.got, "null") {
				t.Errorf("%s marshalled with a null slice: %s", tt.name, tt.got)
			}
		})
	}

	// Direct, unambiguous checks for the two nil-slice-prone fields on VM.
	vm := marshal(t, FromVM(core.VM{Name: "bare"}, true))
	if !strings.Contains(vm, `"recipes":[]`) {
		t.Errorf("VM.recipes did not marshal as []: %s", vm)
	}
	if !strings.Contains(vm, `"forwards":[]`) {
		t.Errorf("VM.forwards did not marshal as []: %s", vm)
	}
}

func TestFromVMStatusRedactsSecrets(t *testing.T) {
	got := FromVMStatus(core.VM{
		Name: "work", OS: "alpine", Mode: "live",
		RecipeStates: []core.RecipeState{{
			Name: "docker", Applied: true, Version: "1.2.0", Health: string(core.HealthOK),
			Params:      map[string]string{"user": "dev", "authkey": core.SecretSet},
			SecretNames: []string{"authkey"},
			Outputs:     map[string]string{"socket": "/var/run/docker.sock"},
		}},
		Health: core.HealthOK,
	}, false)

	if len(got.RecipeStates) != 1 {
		t.Fatalf("recipe states = %#v, want one redacted state", got.RecipeStates)
	}
	p := got.RecipeStates[0].Params
	if p["authkey"] != "<set>" {
		t.Errorf("authkey = %q, want <set>", p["authkey"])
	}
	if p["user"] != "dev" {
		t.Errorf("user = %q, want dev", p["user"])
	}
}

// The wire redactor keys off the manifest's secret-name list, not the value
// core happened to provide: a raw core secret must not cross this boundary.
func TestFromVMStatusRedactsEvenWhenCorePassesARawSecret(t *testing.T) {
	got := FromVMStatus(core.VM{
		Name: "work",
		RecipeStates: []core.RecipeState{{
			Name: "tailscale", Applied: true,
			Params: map[string]string{"authkey": "tskey-SENTINEL"}, SecretNames: []string{"authkey"},
		}},
	}, false)
	if len(got.RecipeStates) != 1 {
		t.Fatalf("recipe states = %#v, want one redacted state", got.RecipeStates)
	}
	if v := got.RecipeStates[0].Params["authkey"]; v != "<set>" {
		t.Errorf("authkey = %q, want <set>", v)
	}
}

func TestFromVMStatusPreservesSetAndUnsetSecretMarkers(t *testing.T) {
	got := FromVMStatus(core.VM{
		Name: "work",
		RecipeStates: []core.RecipeState{{
			Name:        "docker",
			Params:      map[string]string{"set_token": core.SecretSet, "unset_token": core.SecretUnset},
			SecretNames: []string{"set_token", "unset_token"},
		}},
	}, false)
	if len(got.RecipeStates) != 1 {
		t.Fatalf("recipe states = %#v, want one state", got.RecipeStates)
	}
	params := got.RecipeStates[0].Params
	if params["set_token"] != core.SecretSet || params["unset_token"] != core.SecretUnset {
		t.Fatalf("secret markers = %#v, want set/unset markers", params)
	}
}

// Empty status maps marshal as {}, never null: a caller iterating them must
// not branch on a second representation of "no values".
func TestVMStatusEmptyMapsAreObjects(t *testing.T) {
	b, err := json.Marshal(FromVMStatus(core.VM{
		Name: "work", RecipeStates: []core.RecipeState{{Name: "xfce"}},
	}, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(FromVMStatus(core.VM{Name: "work"}, false).RecipeStates) != 0 {
		t.Fatal("empty recipe states were not normalized")
	}
	if !strings.Contains(string(b), `"params":{}`) || !strings.Contains(string(b), `"outputs":{}`) {
		t.Errorf("got %s", b)
	}
}

// --- exec's non-UTF-8 handling (§4) ---

func TestExecResultPlainUTF8(t *testing.T) {
	r := FromExecResult(core.ExecResult{Stdout: "hello\nworld\n", Stderr: "", ExitCode: 0})
	if r.Stdout != "hello\nworld\n" || r.StdoutBase64 != "" || r.StdoutEncoding != "" {
		t.Errorf("valid utf8 stdout should stay plain: %+v", r)
	}
}

func TestExecResultInvalidUTF8UsesBase64(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe, 0x00, 0x01})
	r := FromExecResult(core.ExecResult{Stdout: invalid, ExitCode: 0})
	if r.Stdout != "" {
		t.Errorf("invalid utf8 must not be carried in the plain field: %q", r.Stdout)
	}
	if r.StdoutEncoding != "base64" {
		t.Errorf("stdout_encoding = %q, want base64", r.StdoutEncoding)
	}
	if r.StdoutBase64 == "" {
		t.Error("stdout_base64 must be populated for invalid utf8")
	}
	got := marshal(t, r)
	if strings.Contains(got, `"stdout":`) {
		t.Errorf("plain stdout field must be omitted when base64 is used: %s", got)
	}
}

func TestGuestGolden(t *testing.T) {
	o, ok := guest.Lookup("fedora")
	if !ok {
		t.Fatal("no bundled fedora guest")
	}
	got := marshal(t, FromGuest(o))
	for _, want := range []string{`"name":"fedora"`, `"init":"systemd"`, `"default_backend":"cloudinit"`, `"escalate":["sudo","-n"]`, `"capabilities":["dnf","systemd"]`, `"aliases":["rpm-family"]`, `"pkg":{"setup":"","install":["dnf","install","-y"]`, `"source":"bundled"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "null") {
		t.Errorf("null in %s", got)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
