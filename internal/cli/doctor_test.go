package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func TestDoctorShowsOptionalGitRepairWithoutUnhealthy(t *testing.T) {
	cliRoot(t)
	bin := t.TempDir()
	for _, name := range []string{"qemu-system-x86_64", "qemu-img", "ssh", "xorriso"} {
		path := filepath.Join(bin, name)
		body := "#!/bin/sh\nexit 0\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skipf("ssh-keygen unavailable for CLI setup: %v", err)
	}
	if err := os.Symlink(sshKeygen, filepath.Join(bin, "ssh-keygen")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	checks := core.Doctor()
	var gitFix string
	for _, c := range checks {
		switch c.Name {
		case "git":
			if c.OK {
				t.Fatal("git unexpectedly passed with a PATH containing only required stubs")
			}
			if !c.Optional {
				t.Fatal("Doctor marked missing git as required")
			}
			gitFix = strings.Join(c.Fix, " && ")
		case "/dev/kvm":
			if !c.OK {
				t.Skipf("host does not provide a usable /dev/kvm: %s", c.Detail)
			}
		default:
			if !c.OK {
				t.Fatalf("required check %s failed in isolated PATH: %s", c.Name, c.Detail)
			}
		}
	}
	if gitFix == "" {
		t.Fatal("Doctor omitted the optional git fix")
	}

	var out, errOut bytes.Buffer
	if code := Main([]string{"doctor"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("human doctor exit = %d, want ExitOK; stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if !strings.Contains(strings.ToLower(out.String()), "optional") || !strings.Contains(out.String(), "git") {
		t.Fatalf("human doctor did not identify optional Git: %q", out.String())
	}
	if !strings.Contains(out.String(), gitFix) {
		t.Fatalf("human doctor omitted Git repair command %q: %q", gitFix, out.String())
	}
	if strings.Contains(out.String(), "FAIL:") {
		t.Fatalf("optional Git made a healthy host look failed: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("human doctor stderr = %q", errOut.String())
	}

	var jsonOut, jsonErr bytes.Buffer
	if code := Main([]string{"--json", "doctor"}, "test", nil, &jsonOut, &jsonErr); code != ExitOK {
		t.Fatalf("JSON doctor exit = %d, want ExitOK; stdout=%q stderr=%q", code, jsonOut.String(), jsonErr.String())
	}
	var envelope map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut.Bytes()), &envelope); err != nil {
		t.Fatalf("JSON doctor output is not one envelope: %v; output=%q", err, jsonOut.String())
	}
	data, _ := envelope["data"].(map[string]any)
	if data["healthy"] != true {
		t.Fatalf("JSON doctor healthy = %v, want true with only optional Git missing", data["healthy"])
	}
	rows, _ := data["checks"].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["name"] == "git" {
			if row["optional"] != true {
				t.Fatalf("JSON Git row optional = %v, want true", row["optional"])
			}
			return
		}
	}
	t.Fatal("JSON doctor omitted the Git check")
}
