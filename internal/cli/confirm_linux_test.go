//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/cli/wire"
	"github.com/novusedge/stoat/internal/recipes"
	"github.com/novusedge/stoat/internal/testutil"
	"golang.org/x/sys/unix"
)

func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("cannot query pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("cannot unlock pseudo-terminal: %v", err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open pseudo-terminal slave: %v", err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func TestRecipeURLPreviewUsesManifestFieldsOnATTY(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := testutil.GitRepo(t, map[string]string{
		"recipe.toml": "schema = 3\nname = \"demo\"\ndescription = \"demo recipe\"\nos = [\"alpine\"]\nrequires = [\"git\"]\nscript = \"install.sh\"\n\n[params.channel]\ntype = \"enum\"\nvalues = [\"stable\", \"test\"]\ndefault = \"stable\"\n",
		"install.sh":  "#!/bin/sh\necho demo\n",
	})
	bare := filepath.Join(filepath.Dir(src), "demo.git")
	if err := os.Rename(src, bare); err != nil {
		t.Fatal(err)
	}
	master, tty := openTestPTY(t)
	if _, err := master.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	code := Main([]string{"recipe", "add", bare, "--global"}, "test", tty, tty, &errOut)
	if code != ExitOK {
		t.Fatalf("TTY URL add exit = %d, stderr = %q", code, errOut.String())
	}
	if err := master.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	output := string(buf[:n])
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	start := strings.Index(normalized, "name: demo")
	if start < 0 {
		t.Fatalf("TTY output has no manifest preview: %q", output)
	}
	preview, _, ok := strings.Cut(normalized[start:], "install demo from ")
	if !ok {
		t.Fatalf("TTY output has no confirmation prompt: %q", output)
	}
	if want := "name: demo\nos: alpine\nrequires: git\nparam: channel (enum)\n"; preview != want {
		t.Fatalf("TTY preview = %q, want exactly %q", preview, want)
	}
	if strings.Contains(preview, "description:") {
		t.Fatalf("TTY preview exposed unapproved description field: %q", preview)
	}
	for _, want := range []string{"demo", "alpine", "git", "channel"} {
		if !strings.Contains(output, want) {
			t.Errorf("TTY preview missing %q: %q", want, output)
		}
	}
}

func TestRecipeURLNonTTYFilesRefuseBeforePreviewAndMutation(t *testing.T) {
	cliRoot(t)
	t.Chdir(t.TempDir())
	src := cliRecipeRepo(t, "demo", "#!/bin/sh\necho demo\n")
	trace := filepath.Join(t.TempDir(), "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	cases := []struct {
		name       string
		stdin      func(t *testing.T) *os.File
		stdoutNull bool
		args       []string
	}{
		{name: "dev-null", stdin: func(t *testing.T) *os.File {
			f, err := os.Open("/dev/null")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.Close() })
			return f
		}, stdoutNull: true},
		{name: "pipe", stdin: func(t *testing.T) *os.File {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
			return r
		}},
		{name: "json-dev-null", stdin: func(t *testing.T) *os.File {
			f, err := os.Open("/dev/null")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = f.Close() })
			return f
		}, args: []string{"--json"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(trace)
			root := cliRoot(t)
			t.Chdir(t.TempDir())
			stdin := tc.stdin(t)
			stdoutPath := filepath.Join(t.TempDir(), "stdout")
			var err error
			var stdout *os.File
			if tc.stdoutNull {
				stdout, err = os.OpenFile("/dev/null", os.O_WRONLY, 0)
			} else {
				stdout, err = os.Create(stdoutPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stdout.Close() })
			args := append(append([]string{}, tc.args...), "recipe", "add", src, "--global")
			if tc.name == "pipe" {
				args = append([]string{"--quiet"}, args...)
			}
			var errOut bytes.Buffer
			code := Main(args, "test", stdin, stdout, &errOut)
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			var body []byte
			if !tc.stdoutNull {
				body, err = os.ReadFile(stdoutPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			if code != ExitFail {
				t.Fatalf("non-TTY URL add exit = %d, want ExitFail; stdout=%q stderr=%q", code, body, errOut.String())
			}
			if tc.name == "json-dev-null" {
				var envelope map[string]any
				if err := json.Unmarshal(bytes.TrimSpace(body), &envelope); err != nil {
					t.Fatalf("JSON refusal is not one envelope: %v; output=%q", err, body)
				}
				errObj, _ := envelope["error"].(map[string]any)
				if errObj["code"] != string(wire.CodeConfirmationRequired) {
					t.Fatalf("JSON refusal code = %v, want %q", errObj["code"], wire.CodeConfirmationRequired)
				}
			} else if !tc.stdoutNull && len(body) != 0 {
				t.Fatalf("quiet non-TTY refusal wrote preview/prompt prose: %q", body)
			}
			scope, err := recipes.ScopeFor(true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(scope.LockPath); !os.IsNotExist(err) {
				t.Fatalf("non-TTY refusal mutated global lock under %s: %v", root, err)
			}
			if _, err := os.Stat(trace); err == nil {
				traceBody, readErr := os.ReadFile(trace)
				if readErr != nil || len(traceBody) > 0 {
					t.Fatalf("non-TTY refusal triggered Git preview trace: %q", traceBody)
				}
			}
		})
	}
}
