package cli

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/core"
)

func TestSSHRunsACommand(t *testing.T) {
	cases := []struct {
		argv    []string
		command []string
	}{
		{[]string{"ssh", "web"}, nil},
		{[]string{"ssh", "web", "uptime"}, []string{"uptime"}},
		{[]string{"ssh", "web", "ls", "-la", "/tmp"}, []string{"ls", "-la", "/tmp"}},
		{[]string{"ssh", "web", "--", "ls", "-q"}, []string{"ls", "-q"}},
	}
	for _, c := range cases {
		a, err := Parse(c.argv)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.argv, err)
		}
		if a.Cmd != "ssh" || a.VM != "web" || !slices.Equal(a.Command, c.command) {
			t.Errorf("Parse(%q) = %s %q %q, want ssh web %q", c.argv, a.Cmd, a.VM, a.Command, c.command)
		}
	}
}

func TestSSHCommandKeepsItsOwnJSONFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	// Refused for a different reason than a parse error: the guest's --json
	// must not have switched stoat into JSON mode.
	code := Main([]string{"ssh", "web", "app", "--json"}, "test", strings.NewReader(""), &out, &errOut)
	if strings.Contains(out.String(), `"ok"`) {
		t.Errorf("--json after the command reached stoat (code %d): %s", code, out.String())
	}
}

func TestLogsFlags(t *testing.T) {
	cases := []struct {
		argv   []string
		n      int
		follow bool
	}{
		{[]string{"logs", "web"}, 50, false},
		{[]string{"logs", "web", "-n", "5"}, 5, false},
		{[]string{"logs", "web", "--lines", "6"}, 6, false},
		{[]string{"logs", "web", "--n", "7"}, 7, false},
		{[]string{"logs", "web", "-f"}, 50, true},
		{[]string{"logs", "web", "--follow", "-n", "1"}, 1, true},
	}
	for _, c := range cases {
		a, err := Parse(c.argv)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.argv, err)
		}
		if a.N != c.n || a.Follow != c.follow {
			t.Errorf("Parse(%q): N=%d Follow=%v, want %d %v", c.argv, a.N, a.Follow, c.n, c.follow)
		}
	}
	if a, _ := Parse([]string{"logs", "--help"}); strings.Contains(a.Help, "--n=") {
		t.Errorf("help lists the hidden --n:\n%s", a.Help)
	}
}

func TestLogsNoApplyLogYet(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	if err := (&config.VM{Name: "web", Mode: "live", RAM: 1024, CPUs: 1, SSHPort: 2200}).Save(); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := Main([]string{"logs", "web", "--which", "apply"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "no apply log yet for web") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestLogsFollowJSONRefused(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"--json", "logs", "web", "-f"}, "test", strings.NewReader(""), &out, &errOut); code != ExitUsage {
		t.Errorf("exit %d, want %d: %s", code, ExitUsage, out.String())
	}
}

func TestFollowLog(t *testing.T) {
	var mu sync.Mutex
	content := "one\ntwo\n"
	read := func() ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		return []byte(content), nil
	}
	set := func(s string) {
		mu.Lock()
		content = s
		mu.Unlock()
	}

	var out bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	printed := len(content)
	go func() { done <- followLog(ctx, read, &out, printed, time.Millisecond) }()

	// A partial line waits for its newline; a truncated file starts over.
	set("one\ntwo\nthr")
	time.Sleep(20 * time.Millisecond)
	set("one\ntwo\nthree\n")
	time.Sleep(20 * time.Millisecond)
	set("new\n")
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "three\nnew\n"; got != want {
		t.Errorf("followed %q, want %q", got, want)
	}
}

func TestCreateHelpShowsCoreDefaults(t *testing.T) {
	a, err := Parse([]string{"create", "--help"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"default 4096", "default 4", "default 8G"} {
		if !strings.Contains(a.Help, want) {
			t.Errorf("create --help lacks %q:\n%s", want, a.Help)
		}
	}
	if core.DefaultRAM != 4096 || core.DefaultCPUs != 4 || core.DefaultDisk != "8G" {
		t.Error("core defaults changed: update this test's expectations")
	}
}

func TestInitTemplateUsesCoreDefaults(t *testing.T) {
	body := initTemplate("demo")
	for _, want := range []string{`cpus = 4`, `ram = 4096`, `disk = "8G"`} {
		if !strings.Contains(body, want) {
			t.Errorf("template lacks %q", want)
		}
	}
}

func TestCreateWithoutTTYRecordsVNC(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	haveImage(t, home, "alpine-virt-3.24.1-x86_64.iso")
	var out, errOut bytes.Buffer
	if code := Main([]string{"create", "work", "--image", "alpine-virt-3.24.1-x86_64.iso"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	v, err := config.Load("work")
	if err != nil {
		t.Fatal(err)
	}
	if v.Display != core.DisplayVNC {
		t.Errorf("display = %q, want vnc", v.Display)
	}
}

func TestImagesColumnsFitTheData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	const long = "a-very-long-bring-your-own-image-name-x86_64.iso"
	haveImage(t, home, long)
	var out, errOut bytes.Buffer
	if code := Main([]string{"images"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	col := strings.Index(lines[0], "OS")
	found := false
	for _, l := range lines[1:] {
		if strings.HasPrefix(l, long) {
			found = true
			if l[len(long):col] != strings.Repeat(" ", col-len(long)) {
				t.Errorf("long id runs into the next column: %q", l)
			}
		}
	}
	if !found {
		t.Errorf("long id missing from output:\n%s", out.String())
	}
}

func TestDoctorPrintsOneLinePerCheck(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	Main([]string{"doctor"}, "test", strings.NewReader(""), &out, &errOut)
	var lines int
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "ok: ") || strings.HasPrefix(l, "FAIL: ") || strings.HasPrefix(l, "WARN: ") {
			lines++
		}
	}
	if want := len(core.Doctor()); lines != want {
		t.Errorf("%d check lines, want %d:\n%s", lines, want, out.String())
	}
	out.Reset()
	Main([]string{"-q", "doctor"}, "test", strings.NewReader(""), &out, &errOut)
	if strings.Contains(out.String(), "ok: ") {
		t.Errorf("-q printed passing checks:\n%s", out.String())
	}
}

func TestLoggerDefaultsToInfo(t *testing.T) {
	// The log file is the only observable; a debug line must not reach it.
	home := t.TempDir()
	t.Setenv("STOAT_HOME", home)
	var out, errOut bytes.Buffer
	if code := Main([]string{"logs"}, "test", strings.NewReader(""), &out, &errOut); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	b, _ := os.ReadFile(home + "/logs/stoat.log")
	if strings.Contains(string(b), "DEBU") {
		t.Errorf("debug line in the log:\n%s", b)
	}
}
