package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestParseParamFlagsStaysPure(t *testing.T) {
	t.Setenv("STOAT_SECRET_PARAMDEMO_AUTHKEY", "must-not-be-read-during-parse")
	a, err := Parse([]string{
		"create", "work", "--image", "alpine", "--set", "paramdemo.user=dev",
		"--secret", "paramdemo.authkey",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ParamEdit{
		{Recipe: "paramdemo", Param: "user", Value: "dev"},
		{Recipe: "paramdemo", Param: "authkey", Secret: true},
	}
	if !reflect.DeepEqual(a.Params, want) {
		t.Errorf("Params = %+v, want %+v; parsing must not resolve the secret", a.Params, want)
	}
}

func TestParseUnsetParamFlag(t *testing.T) {
	a, err := Parse([]string{"update", "work", "--unset", "paramdemo.user"})
	if err != nil {
		t.Fatal(err)
	}
	want := []ParamEdit{{Recipe: "paramdemo", Param: "user", Unset: true}}
	if !reflect.DeepEqual(a.Params, want) {
		t.Errorf("Params = %+v, want %+v", a.Params, want)
	}
	if !containsString(a.Changed, "params") {
		t.Errorf("Changed = %v, want params", a.Changed)
	}
}

func TestCLICreateAndUpdatePersistsParamEdits(t *testing.T) {
	dir := cliRoot(t)
	if err := os.MkdirAll(filepath.Join(dir, "isos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "isos", "alpine-virt-3.24.1-x86_64.iso"), []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}
	recipeDir := filepath.Join(dir, "recipes", "paramdemo")
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"paramdemo\"\nscript = \"install.sh\"\n" +
		"[params.user]\ntype = \"string\"\ndefault = \"dev\"\n" +
		"[params.authkey]\ntype = \"secret\"\nrequired = true\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	const secret = "cli-secret-value"
	t.Setenv("STOAT_SECRET_PARAMDEMO_AUTHKEY", secret)
	var out, errOut bytes.Buffer
	if code := Main([]string{
		"--json", "create", "work", "--image", "alpine-virt-3.24.1-x86_64.iso",
		"--recipes", "paramdemo", "--set", "paramdemo.user=alice", "--secret", "paramdemo.authkey",
	}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("create exit %d: stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), secret) {
		t.Fatal("secret appeared in create output")
	}

	out.Reset()
	errOut.Reset()
	if code := Main([]string{
		"--json", "update", "work", "--set", "paramdemo.user=bob", "--unset", "paramdemo.authkey",
	}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("update exit %d: stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	v, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := v.Param("paramdemo", "user"); !ok || got != "bob" {
		t.Errorf("user = %q/%v, want bob/true", got, ok)
	}
	secrets, err := config.LoadSecrets(v.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := secrets["paramdemo"]["authkey"]; ok {
		t.Error("--unset did not remove the secret value")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
