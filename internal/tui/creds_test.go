package tui

import (
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/iso"
)

// TestDetailShowsConsoleCredentials covers the reported problem: launching an
// Ubuntu cloud VM shows a login prompt in the qemu window, and nothing in
// stoat said what to type. cloud-init locks every account by default
// (lock_passwd defaults to true) and the seed sets ssh_pwauth: false, so
// without a password there is no valid answer to that prompt at all.
func TestDetailShowsConsoleCredentials(t *testing.T) {
	withPassword := &config.VM{
		Name: "ubuntu-1", Mode: "cloud", OS: "ubuntu", Backend: "cloudinit",
		RAM: 4096, CPUs: 4, SSHPort: 2202, Dir: t.TempDir(),
		Base: "/tmp/b.qcow2", SSHUser: "stoat", ConsolePassword: "stoat",
	}
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(withPassword)
	out := m.viewDetail()

	if !strings.Contains(out, "console") {
		t.Error("detail pane has no console row")
	}
	if !strings.Contains(out, "stoat / stoat") {
		t.Errorf("detail pane does not show the credential pair:\n%s", out)
	}
	// It must be clear the password is not an ssh credential — the seed
	// refuses password auth over the forwarded port.
	if !strings.Contains(out, "qemu window only") {
		t.Error("detail pane does not say the password is console-only")
	}

	// A cloud VM without one (created before this existed) must say so
	// rather than leaving the user to discover it at a login prompt.
	noPassword := *withPassword
	noPassword.ConsolePassword = ""
	m2 := model{screen: screenDetail, width: 100, height: 40}
	m2.detail = newDetail(&noPassword)
	out2 := m2.viewDetail()
	if !strings.Contains(out2, "not possible") {
		t.Errorf("a cloud VM with no console password does not warn:\n%s", out2)
	}
}

// TestLiveVMNeedsNoConsolePassword: an Alpine live VM already logs root in at
// the console with no password, so inventing one would make things worse, and
// the row should stay out of the way.
func TestLiveVMNeedsNoConsolePassword(t *testing.T) {
	m := model{screen: screenDetail, width: 100, height: 40}
	m.detail = newDetail(&config.VM{
		Name: "live1", Mode: "live", OS: "alpine", ISO: "isos/alpine.iso",
		RAM: 4096, CPUs: 4, SSHPort: 2200, Dir: t.TempDir(),
	})
	out := m.viewDetail()
	if strings.Contains(out, "not possible") {
		t.Errorf("a live VM was warned about console login it already has:\n%s", out)
	}
}

// TestRandomConsolePassword pins the generated form: hex, from crypto/rand,
// and never the same twice.
func TestRandomConsolePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		p, err := config.RandomConsolePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(p) != 32 {
			t.Errorf("password %q is %d chars, want 32", p, len(p))
		}
		if strings.Trim(p, "0123456789abcdef") != "" {
			t.Errorf("password %q is not hex", p)
		}
		if seen[p] {
			t.Fatalf("generated the same password twice: %q", p)
		}
		seen[p] = true
	}
}

// TestFormOffersConsolePasswordOnlyForCloud: the row is meaningless for the
// other backends, which never set one, and a focus stop on an undrawn row is
// a bug class this codebase has already had twice.
func TestFormOffersConsolePasswordOnlyForCloud(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	f := newForm()

	f.images = []imageOption{{
		entry:   &iso.Entry{ID: "u", OS: "ubuntu", Backend: "cloudinit"},
		osName:  "ubuntu",
		backend: "cloudinit",
	}}
	f.imgIdx = 0
	if !containsFocus(f.order(), fPassword) {
		t.Error("a cloud image does not offer the console password row")
	}

	f.images = []imageOption{{file: "alpine.iso", backend: "apkovl", osName: "alpine"}}
	f.imgIdx = 0
	if containsFocus(f.order(), fPassword) {
		t.Error("an apkovl image offers a console password row it will never use")
	}
}

func containsFocus(o focusOrder, want int) bool {
	for _, f := range o {
		if f == want {
			return true
		}
	}
	return false
}
