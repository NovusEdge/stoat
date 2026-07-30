package cloudinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

const testPubkey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMEJWDI8nb2ebdwSCKALxAUfgV97KKvVFxyDf+OnpgKA stoat"

func TestSeedWritesUserDataAndMetaData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)

	v := &config.VM{
		Name: "web1", Mode: "disk", RAM: 2048, CPUs: 2,
		Dir: filepath.Join(root, "web1"),
	}

	isoPath, err := Seed(v, testPubkey)
	if err != nil {
		t.Fatal(err)
	}

	seedDir := filepath.Join(v.OvlDir(), "seed")
	userData, err := os.ReadFile(filepath.Join(seedDir, "user-data"))
	if err != nil {
		t.Fatalf("reading user-data: %v", err)
	}
	ud := string(userData)

	if !strings.HasPrefix(ud, "#cloud-config\n") {
		t.Errorf("user-data does not start with #cloud-config: %q", ud)
	}
	if !strings.Contains(ud, testPubkey) {
		t.Error("user-data missing the pubkey")
	}
	if !strings.Contains(ud, "name: stoat") {
		t.Error("user-data missing the stoat user")
	}
	if !strings.Contains(ud, `sudo: "ALL=(ALL) NOPASSWD:ALL"`) {
		t.Error("user-data missing quoted sudo directive")
	}
	if !strings.Contains(ud, "shell: /bin/bash") {
		t.Error("user-data missing shell directive")
	}
	if !strings.Contains(ud, "ssh_pwauth: false") {
		t.Error("user-data missing ssh_pwauth: false")
	}
	if strings.Contains(ud, "- default") {
		t.Error("user-data must not include the distro default user")
	}

	metaData, err := os.ReadFile(filepath.Join(seedDir, "meta-data"))
	if err != nil {
		t.Fatalf("reading meta-data: %v", err)
	}
	md := string(metaData)
	if !strings.Contains(md, "instance-id: stoat-web1") {
		t.Errorf("meta-data missing instance-id: %q", md)
	}
	if !strings.Contains(md, "local-hostname: web1") {
		t.Errorf("meta-data missing local-hostname: %q", md)
	}

	wantISO := filepath.Join(v.OvlDir(), "seed.iso")
	if isoPath != wantISO {
		t.Errorf("Seed returned %q, want %q", isoPath, wantISO)
	}

	if !haveXorriso() {
		t.Skip("xorriso not available: skipping ISO label assertion")
	}

	f, err := os.Open(isoPath)
	if err != nil {
		t.Fatalf("seed.iso not written: %v", err)
	}
	defer f.Close()

	label := make([]byte, 32)
	if _, err := f.ReadAt(label, 0x8028); err != nil {
		t.Fatalf("reading ISO9660 volume label: %v", err)
	}
	if got := strings.TrimSpace(string(label)); got != "CIDATA" {
		t.Errorf("ISO9660 volume label = %q, want CIDATA", got)
	}
}

func TestHaveXorriso(t *testing.T) {
	// Just exercise the function; result depends on the environment.
	_ = haveXorriso()
}
