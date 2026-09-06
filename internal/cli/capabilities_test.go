package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestCapabilitiesCLIUsesHumanAndJSONForms(t *testing.T) {
	cliRoot(t)

	var humanOut, humanErr bytes.Buffer
	if code := Main([]string{"capabilities"}, "v-test", strings.NewReader(""), &humanOut, &humanErr); code != ExitOK {
		t.Fatalf("human capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, humanOut.String(), humanErr.String())
	}
	if strings.Contains(humanOut.String(), "{") {
		t.Fatalf("human capabilities emitted a JSON object: %q", humanOut.String())
	}
	for _, header := range []string{"NAME", "STATUS", "SCOPE"} {
		if !strings.Contains(strings.ToUpper(humanOut.String()), header) {
			t.Errorf("human capabilities omitted %s header: %q", header, humanOut.String())
		}
	}

	var jsonOut, jsonErr bytes.Buffer
	if code := Main([]string{"--json", "capabilities"}, "v-test", strings.NewReader(""), &jsonOut, &jsonErr); code != ExitOK {
		t.Fatalf("JSON capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, jsonOut.String(), jsonErr.String())
	}
	var envelope struct {
		Cmd  string `json:"cmd"`
		OK   bool   `json:"ok"`
		Data struct {
			Schema int `json:"schema"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &envelope); err != nil {
		t.Fatalf("JSON capabilities output is not one envelope: %v; output=%q", err, jsonOut.String())
	}
	if envelope.Cmd != "capabilities" || !envelope.OK || envelope.Data.Schema != 1 {
		t.Errorf("JSON capabilities envelope = %+v, want cmd=capabilities ok=true data.schema=1", envelope)
	}
}

func TestCapabilitiesGlobalVMInProjectPreservesStalePID(t *testing.T) {
	projectRoot(t, twoVMs)
	if err := (&config.VM{Name: "outside", Mode: "cloud"}).Save(); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(config.Root(), "outside", "qemu.pid")
	want := []byte(strconv.Itoa(os.Getpid()) + "\n")
	if err := os.WriteFile(pidPath, want, 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if code := Main([]string{"capabilities", "outside"}, "v-test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("capabilities global VM exit = %d, want ExitOK; stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	got, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("capabilities changed stale PID: got %q, want %q", got, want)
	}
}

func TestCapabilitiesTargetlessDoesNotInitializeStoatHome(t *testing.T) {
	t.Chdir(t.TempDir())
	root := filepath.Join(t.TempDir(), "stoat-home")
	t.Setenv("STOAT_HOME", root)

	var humanOut, humanErr bytes.Buffer
	if code := Main([]string{"capabilities"}, "v-test", strings.NewReader(""), &humanOut, &humanErr); code != ExitOK {
		t.Fatalf("targetless human capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, humanOut.String(), humanErr.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("targetless human capabilities initialized STOAT_HOME: stat error = %v", err)
	}

	var jsonOut, jsonErr bytes.Buffer
	if code := Main([]string{"--json", "capabilities"}, "v-test", strings.NewReader(""), &jsonOut, &jsonErr); code != ExitOK {
		t.Fatalf("targetless JSON capabilities exit = %d, want ExitOK; stdout=%q stderr=%q", code, jsonOut.String(), jsonErr.String())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("targetless JSON capabilities initialized STOAT_HOME: stat error = %v", err)
	}
}
