package mcpsrv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallWritesEachClientsFile(t *testing.T) {
	for _, c := range []struct {
		client, rel, key string
		project          bool
	}{
		{"claude-code", ".claude.json", "mcpServers", false},
		{"claude-code", ".mcp.json", "mcpServers", true},
		{"claude-desktop", ".config/Claude/claude_desktop_config.json", "mcpServers", false},
		{"cursor", ".cursor/mcp.json", "mcpServers", false},
		{"vscode", ".vscode/mcp.json", "servers", false},
	} {
		home := t.TempDir()
		t.Setenv("HOME", home)
		cwd := t.TempDir()
		chdir(t, cwd)

		report, err := Install(c.client, InstallOpts{Project: c.project})
		if err != nil {
			t.Fatalf("%s: %v", c.client, err)
		}
		base := home
		if c.project || c.client == "vscode" {
			base = cwd
		}
		want := filepath.Join(base, c.rel)
		if report.Path != want {
			t.Errorf("%s: wrote %s, want %s", c.client, report.Path, want)
		}
		raw, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("%s: %v", c.client, err)
		}
		var doc map[string]map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			CWD     string   `json:"cwd"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("%s: %v", c.client, err)
		}
		entry := doc[c.key]["stoat"]
		if !filepath.IsAbs(entry.Command) {
			t.Errorf("%s: command %q is not absolute", c.client, entry.Command)
		}
		if len(entry.Args) != 1 || entry.Args[0] != "mcp" {
			t.Errorf("%s: args = %v, want [mcp]", c.client, entry.Args)
		}
		// cwd is written so project scope applies to the server.
		if entry.CWD != cwd {
			t.Errorf("%s: cwd = %q, want %q", c.client, entry.CWD, cwd)
		}
	}
}

func TestInstallPreservesOtherEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, t.TempDir())
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"theme":"dark","mcpServers":{"other":{"command":"/bin/true"},"stoat":{"command":"/old/stoat"}}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("cursor", InstallOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["theme"] != "dark" {
		t.Error("an unrelated top-level key was dropped")
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Error("an unrelated server entry was dropped")
	}
	stoat := servers["stoat"].(map[string]any)
	if stoat["command"] == "/old/stoat" {
		t.Error("the existing stoat entry was not replaced")
	}
}

func TestInstallPrintTouchesNoFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chdir(t, t.TempDir())
	report, err := Install("cursor", InstallOpts{Print: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.JSON == "" {
		t.Fatal("no JSON to print")
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); !os.IsNotExist(err) {
		t.Fatal("--print wrote a file")
	}
}

func TestInstallRefusesAnUnknownClient(t *testing.T) {
	if _, err := Install("emacs", InstallOpts{}); err == nil {
		t.Fatal("accepted an unknown client")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
