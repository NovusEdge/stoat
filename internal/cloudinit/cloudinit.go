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
	"strings"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

// User is the account the seed creates, and therefore the account anything
// provisioned through this backend must connect as. Cloud images lock root,
// so connecting as anything else fails — sshx defaults an empty VM.SSHUser to
// root, which is exactly the wrong answer here.
//
// Exported so the TUI can record it on the VM instead of repeating the
// literal. userDataTemplate below must name this same user; TestSeedUserMatchesUser
// pins that.
const User = "stoat"

// userDataTemplate declares the account stoat connects as. The shell is not
// a constant: cloud-init's user module fails outright when the shell it is
// told to assign does not exist in the image, so the shell has to match the
// guest (see guestShell). The password block is filled in by
// consolePasswordBlock below.
//
// ssh_pwauth stays false on purpose: the password exists so the VNC console
// is usable -- a cloud VM never gets a qemu window (qemu.NeedsWindow), so
// that socket is the only place a console login happens -- not so the
// forwarded port accepts one. Key-only over the network, password at the
// console.
const userDataTemplate = `#cloud-config
users:
  - name: stoat
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    shell: %s
    ssh_authorized_keys:
      - %s
%sssh_pwauth: false
`

// guestShell is the login shell for the account the seed creates. It must be
// a shell that EXISTS in the target image: cloud-init's user module fails
// outright on a missing shell, leaving no account and no authorized_keys, so
// the only symptom is "Permission denied (publickey)" forever. Boot-tested
// against Alpine's 3.24.1 cloud image: /bin/bash refused every connection,
// /bin/ash connected on the first try. That fact now lives in the guest
// registry; this just reads it.
func guestShell(osName string) string {
	if o, ok := guest.Lookup(osName); ok {
		return o.Shell
	}
	// Unknown or empty OS: an empty OS is reachable for a BYO image stoat
	// couldn't recognise, and every image stoat supports except Alpine ships
	// bash, so bash stays the default for anything not in the registry.
	return "/bin/bash"
}

// extraPackages covers what the base block ASSUMES is present but isn't. The
// users: sudo key writes a sudoers fragment; on Alpine the sudo binary is not
// installed (the cloud-init aport prefers doas), so without this the fragment
// refers to a command that does not exist and every escalating recipe fails.
//
// Returned as a #cloud-config-shaped fragment body, not raw text spliced
// onto the base block: it goes through mergeCloudRecipes alongside any
// recipe bodies so a recipe's own packages: list still ends up as a single,
// valid YAML packages: key instead of two competing ones.
func extraPackages(osName string) string {
	o, ok := guest.Lookup(osName)
	if !ok || len(o.SeedPackages) == 0 {
		// Unknown, empty, or an OS that needs nothing extra: no fragment.
		return ""
	}
	var b strings.Builder
	b.WriteString("packages:\n")
	for _, p := range o.SeedPackages {
		b.WriteString("  - " + p + "\n")
	}
	return b.String()
}

// userData builds the #cloud-config body: the hardware-proven users: block
// (parameterized by the guest's shell), followed by whatever packages:/
// runcmd: the OS needs for itself and the VM's selected cloud recipes ask
// for, merged into a single valid document by mergeCloudRecipes.
func userData(v *config.VM, pubkey string, recipeBodies []string) string {
	base := fmt.Sprintf(userDataTemplate, guestShell(v.OS), pubkey, consolePasswordBlock(v.ConsolePassword))

	bodies := recipeBodies
	if extra := extraPackages(v.OS); extra != "" {
		bodies = append([]string{extra}, bodies...)
	}
	return base + mergeCloudRecipes(bodies)
}

// consolePasswordBlock renders the two lines that make console login work,
// or nothing when no password is set.
//
// It uses plain_text_passwd rather than a hash. cloud-init prefers a hash,
// and for an internet-facing server that is right — but the hash would live
// in the same seed file, in the same data root, next to the private key that
// already grants full access to this VM. Protecting it from someone who can
// read the seed but not the key guards a split that does not occur here, and
// the alternative costs either a crypt(3) dependency or a hard requirement on
// openssl at VM-create time.
func consolePasswordBlock(password string) string {
	if password == "" {
		return ""
	}
	return fmt.Sprintf("    lock_passwd: false\n    plain_text_passwd: %q\n", password)
}

const metaDataTemplate = `instance-id: %q
local-hostname: %q
`

// haveXorriso reports whether the xorriso binary is on PATH.
func haveXorriso() bool {
	_, err := exec.LookPath("xorriso")
	return err == nil
}

// splitYAMLTopLevelKey extracts the value block for a top-level YAML key
// from a flat cloud-config-style document: the "key:" line itself plus
// every following line that is blank or indented, stopping at the next
// non-indented, non-blank line (the next top-level key) or EOF. Returns ""
// if key is not present as a top-level key.
func splitYAMLTopLevelKey(doc, key string) string {
	lines := strings.Split(doc, "\n")
	var block []string
	capturing := false
	for _, line := range lines {
		if !capturing {
			if strings.HasPrefix(line, key+":") {
				capturing = true
				block = append(block, line)
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			block = append(block, line)
			continue
		}
		break
	}
	return strings.TrimRight(strings.Join(block, "\n"), "\n")
}

// yamlListItems returns the item lines under a top-level YAML list key in
// doc, with the "key:" header line itself stripped off. Returns "" if key
// is absent.
func yamlListItems(doc, key string) string {
	block := splitYAMLTopLevelKey(doc, key)
	nl := strings.IndexByte(block, '\n')
	if nl < 0 {
		return ""
	}
	return block[nl+1:]
}

// mergeCloudRecipes splices the packages: and runcmd: lists out of each
// cloud recipe fragment body and concatenates them into single packages:/
// runcmd: sections. Fragments are plain #cloud-config documents (see
// xfce.cloud.yaml); only those two top-level keys are recognized, since
// that is the shape every cloud recipe in this repo uses. Returns "" if no
// fragment contributes anything, so a no-recipe VM's user-data stays
// byte-identical to the hand-verified baseline.
func mergeCloudRecipes(recipeBodies []string) string {
	var packages, runcmd []string
	for _, body := range recipeBodies {
		if p := yamlListItems(body, "packages"); p != "" {
			packages = append(packages, p)
		}
		if r := yamlListItems(body, "runcmd"); r != "" {
			runcmd = append(runcmd, r)
		}
	}
	var out strings.Builder
	if len(packages) > 0 {
		out.WriteString("packages:\n")
		out.WriteString(strings.Join(packages, "\n"))
		out.WriteString("\n")
	}
	if len(runcmd) > 0 {
		out.WriteString("runcmd:\n")
		out.WriteString(strings.Join(runcmd, "\n"))
		out.WriteString("\n")
	}
	return out.String()
}

// Seed writes <v.OvlDir()>/seed/{user-data,meta-data} and builds
// <v.OvlDir()>/seed.iso (ISO9660, volume label CIDATA) via xorriso,
// returning the iso path. recipeBodies are the bodies of v's selected cloud
// recipes (already read by the caller); their packages:/runcmd: sections
// are merged into user-data alongside the fixed, hardware-proven users:
// block — cloud-init's packages: list only runs at first boot, so this is
// how a cloud VM's recipes get applied, unlike the ssh-provisioning path
// used by other backends.
func Seed(v *config.VM, pubkey string, recipeBodies []string) (string, error) {
	if !haveXorriso() {
		return "", fmt.Errorf("xorriso is required for cloud-init provisioning; install libisoburn")
	}

	seedDir := filepath.Join(v.OvlDir(), "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		return "", err
	}

	ud := userData(v, pubkey, recipeBodies)
	if err := os.WriteFile(filepath.Join(seedDir, "user-data"), []byte(ud), 0o644); err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(metaDataTemplate, "stoat-"+v.Name, v.Name)
	if err := os.WriteFile(filepath.Join(seedDir, "meta-data"), []byte(metaData), 0o644); err != nil {
		return "", err
	}

	isoPath := filepath.Join(v.OvlDir(), "seed.iso")
	cmd := exec.Command("xorriso", "-as", "mkisofs", "-o", isoPath, "-V", "CIDATA", "-J", "-r", seedDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("xorriso: %w: %s", err, out)
	}

	return isoPath, nil
}
