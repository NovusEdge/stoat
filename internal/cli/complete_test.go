package cli

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

func TestComplete(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	for _, name := range []string{"web", "worker"} {
		if err := (&config.VM{Name: name, Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		words []string
		want  []string // every entry must appear
		not   []string // no entry may appear
	}{
		{[]string{"lo"}, []string{"logs"}, []string{"up"}},
		{[]string{""}, []string{"up", "completion", "recipe"}, []string{"__complete"}},
		{[]string{"recipe", ""}, []string{"list", "new"}, []string{"up"}},
		{[]string{"up", "w"}, []string{"web", "worker"}, nil},
		{[]string{"up", "we"}, []string{"web"}, []string{"worker"}},
		{[]string{"clone", "web", ""}, nil, []string{"web"}},
		{[]string{"logs", "--"}, []string{"--follow", "--lines", "--which", "--json", "--quiet"}, []string{"--n"}},
		{[]string{"logs", "--which", ""}, []string{"apply", "console"}, nil},
		{[]string{"logs", "--which", "=", "a"}, []string{"apply"}, []string{"console"}},
		{[]string{"logs", "--which=a"}, []string{"--which=apply"}, nil},
		{[]string{"create", "x", "--image", "alpine-v"}, []string{"alpine-virt"}, nil},
		{[]string{"--json", "logs", "w"}, []string{"web"}, nil},
		{[]string{"exec", "web", "-"}, nil, []string{"--help", "--json"}},
		{[]string{"ssh", "web", ""}, nil, []string{"web"}},
		{[]string{"completion", ""}, []string{"bash", "zsh", "fish"}, nil},
	}
	for _, c := range cases {
		got := complete(c.words)
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("complete(%q) = %q, missing %q", c.words, got, w)
			}
		}
		for _, n := range c.not {
			if slices.Contains(got, n) {
				t.Errorf("complete(%q) = %q, should not offer %q", c.words, got, n)
			}
		}
	}
}

func TestCompleteReachesMainWithJSONWord(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"__complete", "--js"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "--json" {
		t.Errorf("got %q, want --json", got)
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, errOut bytes.Buffer
		if code := Main([]string{"completion", shell}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
			t.Fatalf("%s: exit %d: %s", shell, code, errOut.String())
		}
		if !strings.Contains(out.String(), "stoat __complete") {
			t.Errorf("%s script does not call stoat __complete:\n%s", shell, out.String())
		}
	}
	if _, err := Parse([]string{"completion", "powershell"}); err == nil {
		t.Error("an unsupported shell parsed")
	}
}
