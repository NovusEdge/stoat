//go:build !linux

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type nativeCapabilitiesPathState struct {
	directory bool
	contents  []byte
}

func snapshotNativeCapabilitiesRoot(t *testing.T, root string) map[string]nativeCapabilitiesPathState {
	t.Helper()
	state := make(map[string]nativeCapabilitiesPathState)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		item := nativeCapabilitiesPathState{directory: entry.IsDir()}
		if !entry.IsDir() {
			item.contents, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		state[rel] = item
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return state
}

func TestCapabilitiesNativeReadsStoppedMetadataWithoutMutation(t *testing.T) {
	root := cliRoot(t)
	vmDir := filepath.Join(root, "stopped")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vmTOML := []byte("name = \"stopped\"\nmode = \"live\"\nos = \"alpine\"\nsshport = 2200\nagent_access = \"observe\"\n")
	if err := os.WriteFile(filepath.Join(vmDir, "vm.toml"), vmTOML, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "qemu.pid"), []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantState := snapshotNativeCapabilitiesRoot(t, root)

	cases := []struct {
		name string
		args []string
		json bool
	}{
		{name: "targetless human", args: []string{"capabilities"}},
		{name: "targetless JSON", args: []string{"--json", "capabilities"}, json: true},
		{name: "targeted human", args: []string{"capabilities", "stopped"}},
		{name: "targeted JSON", args: []string{"--json", "capabilities", "stopped"}, json: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Main(tc.args, "v-test", strings.NewReader(""), &out, &errOut); code != ExitOK {
				t.Fatalf("capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if tc.json {
				var envelope struct {
					Cmd  string          `json:"cmd"`
					OK   bool            `json:"ok"`
					Data json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &envelope); err != nil {
					t.Fatalf("JSON capabilities output is not one envelope: %v; output=%q", err, out.String())
				}
				if envelope.Cmd != "capabilities" || !envelope.OK {
					t.Fatalf("JSON capabilities envelope = %+v, want cmd=capabilities ok=true", envelope)
				}
				var report struct {
					Target *struct {
						Name string `json:"name"`
					} `json:"target"`
				}
				if err := json.Unmarshal(envelope.Data, &report); err != nil {
					t.Fatalf("JSON capabilities data is not a report: %v; data=%s", err, envelope.Data)
				}
				if strings.HasPrefix(tc.name, "targeted") && (report.Target == nil || report.Target.Name != "stopped") {
					t.Errorf("targeted report = %+v, want stopped target", report.Target)
				}
				if strings.HasPrefix(tc.name, "targetless") && report.Target != nil {
					t.Errorf("targetless report target = %+v, want nil", report.Target)
				}
			} else if !strings.Contains(strings.ToUpper(out.String()), "NAME") {
				t.Errorf("human capabilities omitted NAME header: %q", out.String())
			}
			if got := snapshotNativeCapabilitiesRoot(t, root); !reflect.DeepEqual(got, wantState) {
				t.Errorf("capabilities mutated stored root:\n before: %#v\n after:  %#v", wantState, got)
			}
		})
	}
}
