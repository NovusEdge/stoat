package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

// --- golden-file style: marshal each DTO and pin the exact JSON shape. ---

func TestVMGolden(t *testing.T) {
	got := marshal(t, FromVM(sampleVM()))
	want := `{"name":"work","os":"alpine","mode":"live","backend":"apkovl","state":"running","cpus":4,"ram_mb":4096,"disk":"8G","share":"/home/u/src","recipes":["xfce.alpine.sh"],"ssh_port":2222,"ssh_user":"root","installed":false,"forwards":[{"host_port":8080,"guest_port":80}],"allow_exec":true,"display":"vnc"}`
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
	got := marshal(t, FromVM(fresh))
	if !strings.Contains(got, `"display":"window"`) {
		t.Errorf("a VM mid-install has a real window: %s", got)
	}
	installed := fresh
	installed.Installed = true
	got = marshal(t, FromVM(installed))
	if !strings.Contains(got, `"display":"vnc"`) {
		t.Errorf("an installed disk VM is headless: %s", got)
	}
	if strings.Contains(got, "vnc.sock") || strings.Contains(got, "/home/u") {
		t.Errorf("the socket path reached the wire: %s", got)
	}
}

// A broken vm.toml supplies neither mode nor installed, so there is no rule
// to run and "vnc" would be a guess dressed as a fact.
func TestVMBrokenHasNoDisplay(t *testing.T) {
	got := marshal(t, FromVM(core.VM{Name: "wreck", State: core.StateBroken, Error: "bad"}))
	if !strings.Contains(got, `"display":""`) {
		t.Errorf("broken VM must not claim a display surface: %s", got)
	}
}

func TestVMBrokenCarriesError(t *testing.T) {
	v := core.VM{Name: "oldvm", State: core.StateBroken, Error: "broken vm.toml: oldvm: toml: line 4: ..."}
	got := marshal(t, FromVM(v))
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
	got := marshal(t, FromVM(v))
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
	want := `{"name":"qemu-img","ok":false,"detail":"not found","fix":["sudo","pacman","-S","qemu-img"]}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRecipeGolden(t *testing.T) {
	r := core.Recipe{Name: "xfce.alpine.sh", Label: "xfce", TargetOS: "alpine", Shared: false}
	got := marshal(t, FromRecipe(r))
	want := `{"name":"xfce.alpine.sh","label":"xfce","target_os":"alpine","shared":false}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestRecipeIssueGolden(t *testing.T) {
	i := core.RecipeIssue{Name: "xfce.cloud.yaml", Reason: "xfce.cloud.yaml is not offered to alpine/cloudinit"}
	got := marshal(t, FromRecipeIssue(i))
	want := `{"name":"xfce.cloud.yaml","reason":"xfce.cloud.yaml is not offered to alpine/cloudinit"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// --- the three MUST-hold rules from the brief ---

func TestVMNeverSerializesConsolePassword(t *testing.T) {
	got := marshal(t, FromVM(sampleVM()))
	if strings.Contains(strings.ToLower(got), "password") {
		t.Fatalf("VM DTO leaked a password-shaped field: %s", got)
	}
	if strings.Contains(got, "hunter2password") {
		t.Fatalf("VM DTO leaked the actual console password value: %s", got)
	}
}

func TestVMNeverSerializesHostPaths(t *testing.T) {
	got := marshal(t, FromVM(sampleVM()))
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
		{"VM.forwards/recipes", marshal(t, FromVM(core.VM{Name: "bare"}))},
		{"VMs list", marshal(t, FromVMs(nil))},
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
	vm := marshal(t, FromVM(core.VM{Name: "bare"}))
	if !strings.Contains(vm, `"recipes":[]`) {
		t.Errorf("VM.recipes did not marshal as []: %s", vm)
	}
	if !strings.Contains(vm, `"forwards":[]`) {
		t.Errorf("VM.forwards did not marshal as []: %s", vm)
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

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
