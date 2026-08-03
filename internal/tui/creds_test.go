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
	// refuses password auth over the forwarded port. Cloud VMs never get a
	// qemu window (qemu.NeedsWindow), so the row must point at the vnc
	// socket the detail screen also surfaces, not a window that never
	// appears — see IMPORTANT 3 in the final review.
	if !strings.Contains(out, "vnc") {
		t.Error("detail pane does not say the password is reached over vnc")
	}
	if strings.Contains(out, "qemu window only") {
		t.Error("cloud VMs never get a qemu window; the console row must not claim one")
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

// TestCloudVMGetsADiskSize is the regression test for a reported failure that
// looked like a broken recipe and was not. A CoW overlay inherits its BASE
// image's virtual size, and cloud images are sized to boot and nothing more —
// Ubuntu 24.04's is 3.5G with a 2.4G root. Installing a desktop filled it,
// apt exited 100, and cloud-init reported a bare "error" with no mention of
// disk space anywhere the user could see.
func TestCloudVMGetsADiskSize(t *testing.T) {
	t.Setenv("STOAT_HOME", t.TempDir())
	f := newForm()
	f.inputs[fName].SetValue("ubuntu-1")
	// A real catalog entry, with its image on disk: core resolves the entry by
	// ID and reads the backend/OS/ssh user the CATALOG states rather than
	// re-inferring them from the filename.
	entry := iso.Catalog()[0]
	if entry.ID != "ubuntu-24.04" {
		t.Fatalf("catalog[0] = %q, want ubuntu-24.04", entry.ID)
	}
	opt := stubImage(t, "ubuntu-24.04-server-cloudimg-amd64.img")
	opt.entry = &entry
	opt.sshUser = entry.SSHUser
	f.images = []imageOption{opt}
	f.imgIdx = 0

	vm, err := f.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if vm.Mode != "cloud" {
		t.Fatalf("mode = %q, want cloud", vm.Mode)
	}
	if vm.Disk == "" {
		t.Error("a cloud VM was built with no disk size — its overlay would inherit the base image's, which cannot fit a desktop")
	}

	// And the size row must be reachable, or the user cannot change it.
	if !containsFocus(f.order(), fDisk) {
		t.Errorf("the disk row is not in the cloud focus order: %v", f.order())
	}
}
