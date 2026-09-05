package sshx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestParseOutputs(t *testing.T) {
	declared := map[string]string{"socket": "path of the docker socket"}
	body := "socket=/var/run/docker.sock\nsock=/nope\n\n# a comment\nempty=\n"
	vals, undeclared := ParseOutputs(declared, body)

	if vals["socket"] != "/var/run/docker.sock" {
		t.Errorf("socket = %q", vals["socket"])
	}
	if vals["sock"] != "/nope" {
		t.Errorf("an undeclared output was dropped: %v", vals)
	}
	if len(undeclared) != 2 || !containsOutputName(undeclared, "sock") || !containsOutputName(undeclared, "empty") {
		t.Errorf("undeclared = %v, want sock and empty", undeclared)
	}
	if v, ok := vals["empty"]; !ok || v != "" {
		t.Errorf("an empty value was dropped: %v", vals)
	}
	if _, ok := vals["# a comment"]; ok {
		t.Error("a comment line became an output")
	}
}

func TestParseOutputsSplitsAtTheFirstEquals(t *testing.T) {
	vals, _ := ParseOutputs(map[string]string{"dsn": ""}, "dsn=postgres://u:p@h/db?a=b\n")
	if vals["dsn"] != "postgres://u:p@h/db?a=b" {
		t.Errorf("dsn = %q", vals["dsn"])
	}
}

func TestParseOutputsStoresUndeclaredWithEmptyDeclarationMap(t *testing.T) {
	vals, undeclared := ParseOutputs(map[string]string{}, "socket=/var/run/docker.sock\n")
	if vals["socket"] != "/var/run/docker.sock" {
		t.Fatalf("socket = %q, want the emitted value", vals["socket"])
	}
	if len(undeclared) != 1 || undeclared[0] != "socket" {
		t.Fatalf("undeclared = %v, want [socket]", undeclared)
	}
}

func TestProvisionKeepsSecretOutOfSSHArgvAndLogs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	secret := "value with spaces 'quotes'; printf hacked"
	valueFile := filepath.Join(root, "work", "guest-param")
	installSSHRecipe(t, root, "docker", "schema = 3\nname = \"docker\"\nscript = \"install.sh\"\n[params.authkey]\ntype = \"secret\"\nrequired = true\n", "#!/bin/sh\nprintf '%s' \"$STOAT_PARAM_AUTHKEY\" > "+shellQuoteForTest(valueFile)+"\n")

	vmDir := filepath.Join(root, "work")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(vmDir, "ssh-capture")
	installSecretCheckingSSH(t, capture, valueFile, secret, true)
	port := acceptOnly(t, "SSH-2.0-fake\r\n")
	v := &config.VM{
		Name: "work", Dir: vmDir, OS: "alpine", SSHPort: port,
		Recipes: []string{"docker"}, Applied: map[string]config.AppliedRecipe{},
	}
	if err := config.SaveSecrets(vmDir, config.Secrets{"docker": {"authkey": secret}}); err != nil {
		t.Fatal(err)
	}

	err := Provision(context.Background(), v)
	if err == nil {
		t.Fatal("Provision succeeded, want the fake recipe failure")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("secret leaked in Provision error: %v", err)
	}
	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), secret) {
		t.Fatalf("secret leaked in provision log:\n%s", log)
	}
	captured, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	text := string(captured)
	if !strings.Contains(text, secret) {
		t.Fatalf("fake ssh did not observe the secret in stdin:\n%s", text)
	}
	value, err := os.ReadFile(valueFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != secret {
		t.Fatalf("secret changed while crossing the shell boundary: got %q, want %q", value, secret)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "ARGV:") && strings.Contains(line, "cli-secret-value") {
			t.Fatalf("secret appeared in ssh argv: %q", line)
		}
	}
}

func TestProvisionStoresUndeclaredOutputsEvenWhenManifestDeclaresNone(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	const secret = "output-secret-value"
	installSSHRecipe(t, root, "docker", "schema = 3\nname = \"docker\"\nscript = \"install.sh\"\n[params.authkey]\ntype = \"secret\"\nrequired = true\n", "#!/bin/sh\necho provision\n")

	vmDir := filepath.Join(root, "work")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installOutputSSH(t, "rogue="+secret+"\n")
	port := acceptOnly(t, "SSH-2.0-fake\r\n")
	v := &config.VM{
		Name: "work", Dir: vmDir, OS: "alpine", SSHPort: port,
		Recipes: []string{"docker"}, Applied: map[string]config.AppliedRecipe{},
	}
	if err := config.SaveSecrets(vmDir, config.Secrets{"docker": {"authkey": secret}}); err != nil {
		t.Fatal(err)
	}

	if err := Provision(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	got, ok := v.Applied["docker"].Outputs["rogue"]
	if !ok {
		t.Fatalf("undeclared output was discarded: %v", v.Applied["docker"].Outputs)
	}
	if got == secret || strings.Contains(got, secret) {
		t.Fatalf("secret leaked into captured output: %q", got)
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(vmDir, "vm.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), secret) {
		t.Fatal("secret leaked into vm.toml through captured output")
	}
	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), `docker: output "rogue" is not declared`) {
		t.Fatalf("missing undeclared-output warning:\n%s", log)
	}
}

func installSSHRecipe(t *testing.T, root, name, manifest, body string) {
	t.Helper()
	dir := filepath.Join(root, "recipes", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func installSecretCheckingSSH(t *testing.T, capture, valueFile, secret string, failRecipe bool) {
	t.Helper()
	bin := t.TempDir()
	fail := ""
	if failRecipe {
		fail = "printf '%s' \"$(cat " + shellQuoteForTest(valueFile) + ")\"; printf '%s\\n' '-tail'; exit 1\n"
	}
	script := "#!/bin/sh\n" +
		"input=$(cat)\n" + "printf 'ARGV:%s\\n' \"$*\" >> " + shellQuoteForTest(capture) + "\n" +
		"printf 'STDIN:%s\\n' \"$input\" >> " + shellQuoteForTest(capture) + "\n" +
		"case \"$input\" in *STOAT_RECIPE=docker*)\n" +
		"printf '%s' \"$input\" | sh -s\n" + fail + ";; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func containsOutputName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func installOutputSSH(t *testing.T, output string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"input=$(cat)\n" +
		"case \"$*\" in *'cat /tmp/.stoat-out/docker'*) printf '%s' " + shellQuoteForTest(output) + ";; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
