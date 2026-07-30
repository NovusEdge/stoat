package cli

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    *Args
		wantErr bool
	}{
		{"no args", nil, nil, true},
		{"unknown subcommand", []string{"frobnicate"}, nil, true},

		{"ls", []string{"ls"}, &Args{Cmd: "ls"}, false},
		{"ls quiet", []string{"ls", "-q"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls --quiet", []string{"ls", "--quiet"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls --no-interactive", []string{"ls", "--no-interactive"}, &Args{Cmd: "ls", Quiet: true}, false},
		{"ls unexpected arg", []string{"ls", "extra"}, nil, true},

		{"doctor", []string{"doctor"}, &Args{Cmd: "doctor"}, false},
		{"version", []string{"version"}, &Args{Cmd: "version"}, false},
		{"help", []string{"help"}, &Args{Cmd: "help"}, false},

		{"up", []string{"up", "alpine"}, &Args{Cmd: "up", VM: "alpine"}, false},
		{"up missing name", []string{"up"}, nil, true},
		{"up too many args", []string{"up", "a", "b"}, nil, true},
		{"up quiet", []string{"up", "-q", "alpine"}, &Args{Cmd: "up", VM: "alpine", Quiet: true}, false},

		{"down", []string{"down", "alpine"}, &Args{Cmd: "down", VM: "alpine"}, false},
		{"down missing name", []string{"down"}, nil, true},

		{"ssh", []string{"ssh", "alpine"}, &Args{Cmd: "ssh", VM: "alpine"}, false},
		{"ssh missing name", []string{"ssh"}, nil, true},

		{"provision", []string{"provision", "alpine"}, &Args{Cmd: "provision", VM: "alpine"}, false},
		{"provision missing name", []string{"provision"}, nil, true},
		{"provision quiet alias", []string{"provision", "--no-interactive", "alpine"}, &Args{Cmd: "provision", VM: "alpine", Quiet: true}, false},

		{"rm", []string{"rm", "alpine"}, &Args{Cmd: "rm", VM: "alpine"}, false},
		{"rm -y", []string{"rm", "-y", "alpine"}, &Args{Cmd: "rm", VM: "alpine", Yes: true}, false},
		{"rm too many args", []string{"rm", "alpine", "-y"}, nil, true}, // flag stops parsing at first positional
		{"rm missing name", []string{"rm"}, nil, true},
		{"rm quiet and yes", []string{"rm", "-q", "-y", "alpine"}, &Args{Cmd: "rm", VM: "alpine", Quiet: true, Yes: true}, false},

		{"logs default", []string{"logs"}, &Args{Cmd: "logs", N: 50}, false},
		{"logs -n", []string{"logs", "-n", "10"}, &Args{Cmd: "logs", N: 10}, false},
		{"logs unexpected arg", []string{"logs", "extra"}, nil, true},
		{"logs bad -n", []string{"logs", "-n", "notanumber"}, nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Parse(%v) = %+v, nil; want error", c.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%v) unexpected error: %v", c.args, err)
			}
			if *got != *c.want {
				t.Fatalf("Parse(%v) = %+v; want %+v", c.args, got, c.want)
			}
		})
	}
}

// TestParsePure guards against Parse doing anything beyond interpreting
// argv: it must never touch the filesystem, so calling it with a bogus
// STOAT_HOME must not error or panic.
func TestParsePure(t *testing.T) {
	t.Setenv("STOAT_HOME", "/nonexistent/definitely-not-there")
	if _, err := Parse([]string{"ls"}); err != nil {
		t.Fatalf("Parse must not touch disk, got error: %v", err)
	}
}

func TestMainExitCodes(t *testing.T) {
	var out, errOut discard

	if code := Main([]string{"help"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("help: exit %d, want %d", code, ExitOK)
	}
	if code := Main([]string{"version"}, "test", nil, &out, &errOut); code != ExitOK {
		t.Fatalf("version: exit %d, want %d", code, ExitOK)
	}
	if code := Main([]string{"bogus"}, "test", nil, &out, &errOut); code != ExitUsage {
		t.Fatalf("bogus subcommand: exit %d, want %d", code, ExitUsage)
	}
	if code := Main([]string{"up"}, "test", nil, &out, &errOut); code != ExitUsage {
		t.Fatalf("up with no name: exit %d, want %d", code, ExitUsage)
	}
}

// discard is a minimal io.Writer sink so tests don't need os.Stdout.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
