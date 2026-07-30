// Package cloudinit builds a NoCloud cloud-init seed (user-data + meta-data)
// for cloud qcow2 images that expect a distro-default-user setup rather than
// the Alpine-apkovl or bare-ssh mechanisms used elsewhere in stoat.
//
// The seed is packed into an ISO9660 image labeled CIDATA via xorriso, which
// NoCloud's datasource scans for at boot (matched case-insensitively). This
// exact shape — including the quoted sudo string and the xorriso invocation
// below — was hand-verified against a real Ubuntu 24.04 cloud image; do not
// change it without re-verifying on hardware.
package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/novusedge/stoat/internal/config"
)

const userDataTemplate = `#cloud-config
users:
  - name: stoat
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    shell: /bin/bash
    ssh_authorized_keys:
      - %s
ssh_pwauth: false
`

const metaDataTemplate = `instance-id: stoat-%s
local-hostname: %s
`

// haveXorriso reports whether the xorriso binary is on PATH.
func haveXorriso() bool {
	_, err := exec.LookPath("xorriso")
	return err == nil
}

// Seed writes <v.OvlDir()>/seed/{user-data,meta-data} and builds
// <v.OvlDir()>/seed.iso (ISO9660, volume label CIDATA) via xorriso,
// returning the iso path. If xorriso is unavailable, it falls back to
// returning the seed directory itself (no ISO), which a caller can use as a
// vvfat-style drive; that path is distinguishable from the ISO case because
// it names a directory, not a file ending in ".iso".
func Seed(v *config.VM, pubkey string) (string, error) {
	seedDir := filepath.Join(v.OvlDir(), "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		return "", err
	}

	userData := fmt.Sprintf(userDataTemplate, pubkey)
	if err := os.WriteFile(filepath.Join(seedDir, "user-data"), []byte(userData), 0o644); err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(metaDataTemplate, v.Name, v.Name)
	if err := os.WriteFile(filepath.Join(seedDir, "meta-data"), []byte(metaData), 0o644); err != nil {
		return "", err
	}

	if !haveXorriso() {
		// No xorriso: fall back to handing back the seed directory so the
		// caller can attach it as a vvfat drive instead of an ISO.
		return seedDir, nil
	}

	isoPath := filepath.Join(v.OvlDir(), "seed.iso")
	cmd := exec.Command("xorriso", "-as", "mkisofs", "-o", isoPath, "-V", "CIDATA", "-J", "-r", seedDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xorriso: %w: %s", err, out)
	}

	return isoPath, nil
}
