// Package cloudinit builds a NoCloud cloud-init seed (user-data + meta-data)
// for cloud qcow2 images that expect a distro-default-user setup rather than
// the Alpine-apkovl or bare-ssh mechanisms used elsewhere in stoat.
//
// The seed is packed into an ISO9660 image labeled CIDATA via xorriso, which
// NoCloud's datasource scans for at boot (matched case-insensitively). This
// exact shape, including the quoted sudo string and the xorriso invocation
// below, was hand-verified against a real Ubuntu 24.04 cloud image; do not
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
	"gopkg.in/yaml.v3"
)

// User is the account the seed creates, and therefore the account anything
// provisioned through this backend must connect as. Cloud images lock root,
// so connecting as anything else fails: sshx defaults an empty VM.SSHUser to
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
// is usable, since a cloud VM never gets a qemu window (qemu.NeedsWindow), so
// that socket is the only place a console login happens, not so the
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
// Returned as its own #cloud-config-shaped fragment body, not raw text
// spliced onto the base block: it becomes its own document in the
// cloud-config-archive (see buildArchive), alongside the base users: block
// and any recipe bodies, so a recipe's own packages: list still ends up
// merged with this one instead of one silently overwriting the other.
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

// userData builds the seed's user-data as a cloud-config-archive: the
// hardware-proven users: block (parameterized by the guest's shell), the
// OS's own extra packages if it needs any, and every selected cloud
// recipe's body verbatim, each as its own document. cloud-init merges the
// documents itself (see buildArchive), so this package no longer parses any
// of them for packages:/runcmd:, so a fragment using write_files: or any
// other key survives instead of being silently dropped.
func userData(v *config.VM, pubkey string, recipeBodies []string) (string, error) {
	base := fmt.Sprintf(userDataTemplate, guestShell(v.OS), pubkey, consolePasswordBlock(v.ConsolePassword))

	docs := []string{base, mountsDoc(v)}
	if extra := extraPackages(v.OS); extra != "" {
		docs = append(docs, extra)
	}
	docs = append(docs, recipeBodies...)

	return buildArchive(docs)
}

// mountsDoc mounts the 9p exports. Cloud VMs previously got the exports on the
// QEMU command line and nothing ever mounted them, so the share silently did
// nothing.
//
// nofail is required: some cloud kernels ship no 9p module at all (Debian's
// does not), and without it an unmountable share holds up boot. ro on the host
// mount matches what QEMU enforces, so a write fails immediately instead of
// after a remount that appears to succeed.
func mountsDoc(v *config.VM) string {
	const opts = "trans=virtio,version=9p2000.L,%s,_netdev,nofail"
	var b strings.Builder
	b.WriteString("#cloud-config\nmounts:\n")
	b.WriteString(fmt.Sprintf("  - [ work, /mnt/work, 9p, %q, \"0\", \"0\" ]\n", fmt.Sprintf(opts, "rw")))
	if v.Share != "" {
		b.WriteString(fmt.Sprintf("  - [ host, /mnt/host, 9p, %q, \"0\", \"0\" ]\n", fmt.Sprintf(opts, "ro")))
	}
	return b.String()
}

// consolePasswordBlock renders the two lines that make console login work,
// or nothing when no password is set.
//
// It uses plain_text_passwd rather than a hash. cloud-init prefers a hash,
// and for an internet-facing server that is right, but the hash would live
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

// haveCloudInit reports whether the cloud-init binary is on PATH. Mirrors
// haveXorriso above: Arch does not install cloud-init by default (see
// guest-subsystem.md §10), so schema validation must degrade to "not
// checked" rather than "assumed valid"; callers of ValidateFragment must
// treat a nil error with no annotated output as "not checked", not "passed".
func haveCloudInit() bool {
	_, err := exec.LookPath("cloud-init")
	return err == nil
}

// ValidateFragment runs `cloud-init schema -c FILE --annotate` against a
// single #cloud-config document, offline and before boot, and returns the
// annotated output. It returns ("", nil) when cloud-init is not installed;
// the caller must not treat that as "valid", only as "unchecked".
//
// `cloud-init schema` validates one cloud-config document, not a
// cloud-config-archive, so this takes a single fragment body: callers
// validate each recipe body before it is merged into buildArchive's output.
func ValidateFragment(body string) (annotated string, err error) {
	if !haveCloudInit() {
		return "", nil
	}

	f, err := os.CreateTemp("", "stoat-cloudinit-schema-*.yaml")
	if err != nil {
		return "", fmt.Errorf("creating schema-check temp file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return "", fmt.Errorf("writing schema-check temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("writing schema-check temp file: %w", err)
	}

	out, err := exec.Command("cloud-init", "schema", "-c", f.Name(), "--annotate").CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("cloud-init schema: %w", err)
	}
	return string(out), nil
}

// mergeHow is the explicit merge directive every document in the archive
// carries. cloud-init's default merge is dict(no_replace)+list()+str(): two
// documents both declaring packages: do NOT append, the first one wins and
// the second is silently discarded. append+recurse_list makes list values
// (packages:, runcmd:, ...) concatenate instead.
const mergeHow = "list(append)+dict(recurse_list)"

// withMergeHow injects merge_how as a top-level key into a #cloud-config
// document body.
//
// Per cloud-init's merge model (merging.rst, "Specifying multiple types"), a
// document's OWN merge_how does not govern how it merges in: it governs
// how the NEXT document in the archive merges into the accumulated result.
// The first document is always merged with the built-in default regardless
// of what it declares. So to guarantee every later document appends rather
// than silently losing to an earlier one, every document except the last
// needs the directive, and since callers may pass any number of recipe
// bodies, the last one isn't known in advance, so every document gets it.
// This matches cloud-init's own worked example in merging.rst, which puts
// merge_how in both halves of a two-document merge rather than relying on
// which one happens to be last.
func withMergeHow(doc string) string {
	directive := fmt.Sprintf("merge_how: %q\n", mergeHow)
	if rest, ok := strings.CutPrefix(doc, "#cloud-config\n"); ok {
		return "#cloud-config\n" + directive + rest
	}
	return directive + doc
}

// archiveDoc is one entry of a cloud-config-archive: cloud-init's own
// format for a list of {type, content} documents that it merges itself,
// replacing the packages:/runcmd:-only splicing this package used to do by
// hand (see doc/examples/cloud-config-archive.txt upstream).
type archiveDoc struct {
	Type    string `yaml:"type"`
	Content string `yaml:"content"`
}

// buildArchive renders docs as a cloud-config-archive. The
// "#cloud-config-archive" header is required verbatim on its own first
// line: it is what NoCloud's format-detection matches on to unpack the
// payload as an archive rather than parse it (and fail) as one big
// #cloud-config document. Each doc is carried through withMergeHow so the
// archive as a whole merges by appending rather than by cloud-init's
// default first-one-wins.
func buildArchive(docs []string) (string, error) {
	items := make([]archiveDoc, len(docs))
	for i, d := range docs {
		items[i] = archiveDoc{Type: "text/cloud-config", Content: withMergeHow(d)}
	}
	out, err := yaml.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("marshaling cloud-config-archive: %w", err)
	}
	return "#cloud-config-archive\n" + string(out), nil
}

// Seed writes <v.OvlDir()>/seed/{user-data,meta-data} and builds
// <v.OvlDir()>/seed.iso (ISO9660, volume label CIDATA) via xorriso,
// returning the iso path. recipeBodies are the bodies of v's selected cloud
// recipes (already read by the caller); their packages:/runcmd: sections
// are merged into user-data alongside the fixed, hardware-proven users:
// block, because cloud-init's packages: list only runs at first boot, so this is
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

	ud, err := userData(v, pubkey, recipeBodies)
	if err != nil {
		return "", err
	}
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
