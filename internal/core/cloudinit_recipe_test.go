package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestApplyDiscoversCloudInitOutputsAndSkipsTheRecipe(t *testing.T) {
	dir := root(t)
	recipeDir := filepath.Join(dir, "recipes", "docker")
	if err := os.MkdirAll(recipeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema = 3\nname = \"docker\"\nscript = \"install.sh\"\n\n[outputs]\nsocket = \"path\"\n"
	if err := os.WriteFile(filepath.Join(recipeDir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "install.sh"), []byte("#!/bin/sh\necho should-not-run\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	vmDir := filepath.Join(dir, "cloudy")
	port, stopSSH := fakeSSHD(t, 0)
	defer stopSSH()
	v := &config.VM{
		Name: "cloudy", Dir: vmDir, Mode: "cloud", Backend: "cloudinit", OS: "alpine",
		RAM: 512, CPUs: 1, SSHPort: port, Recipes: []string{"docker"},
	}
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	defer fakeRunning(t, v)()
	count := filepath.Join(vmDir, "recipe-count")
	installCloudMarkerSSH(t, count)

	if err := Apply(context.Background(), v.Name, ApplyOpts{}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(v.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Applied["docker"].Outputs["socket"] != "/var/run/docker.sock" {
		t.Fatalf("cloud-init outputs = %v, want socket output", got.Applied["docker"].Outputs)
	}
	b, err := os.ReadFile(count)
	if err == nil && strings.Contains(string(b), "STOAT_RECIPE=docker") {
		t.Fatalf("cloud-init marker discovery reran the already-applied recipe: %s", b)
	}
}

func installCloudMarkerSSH(t *testing.T, count string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ninput=$(cat)\ncase \"$*\" in *'.applied'*) printf '===docker\\nsocket=/var/run/docker.sock\\n';; esac\ncase \"$input\" in *'STOAT_RECIPE=docker'*) printf '%s\\n' \"$input\" >> " + shellQuoteCoreTest(count) + ";; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuoteCoreTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `\'\''`) + "'"
}
