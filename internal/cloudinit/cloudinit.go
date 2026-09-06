// Package cloudinit builds a NoCloud cloud-init seed (user-data + meta-data)
// for cloud qcow2 images. These images expect a distro-default-user setup,
// not the Alpine-apkovl or bare-ssh mechanisms stoat uses elsewhere.
//
// The seed is packed into an ISO9660 image labeled CIDATA, via xorriso.
// NoCloud's datasource scans for that label at boot, case-insensitively.
// The exact shape below, including the quoted sudo string and the xorriso
// invocation, was hand-verified against a real Ubuntu 24.04 cloud image.
// Re-verify on hardware before changing it.
package cloudinit

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

// User is the account the seed creates. Anything provisioned through this
// backend must connect as User. Cloud images lock root, so sshx's default
// of root for an empty VM.SSHUser is the wrong answer here.
//
// Exported so the TUI can record it on the VM instead of repeating the
// literal. userDataTemplate below must name this same user;
// TestSeedUserMatchesUser pins that.
const User = "stoat"

// userDataTemplate declares the account stoat connects as. The shell is not
// a constant: cloud-init's user module fails outright if the assigned shell
// does not exist in the image. The shell must match the guest; see
// guestShell. consolePasswordBlock below fills in the password block.
//
// ssh_pwauth stays false on purpose. The password exists only for the
// console, a qemu window on a graphical host or the VNC socket otherwise
// (qemu.NeedsWindow), the only place a password login happens. The network
// stays key-only.
const userDataTemplate = `#cloud-config
users:
  - name: stoat
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    shell: %s
    ssh_authorized_keys:
      - %s
%sssh_pwauth: false
`

// guestShell is the login shell for the account the seed creates. It must
// be a shell that exists in the target image. cloud-init's user module
// fails outright on a missing shell: no account gets created, and no
// authorized_keys either. The only symptom is "Permission denied
// (publickey)" forever.
//
// Alpine's 3.24.1 cloud image refused every connection with /bin/bash and
// connected on the first try with /bin/ash. That fact lives in the guest
// registry; this function only reads it.
func guestShell(osName string) string {
	if o, ok := guest.Lookup(osName); ok {
		return o.Shell
	}
	// Unknown or empty OS: an empty OS is reachable for a BYO image stoat
	// couldn't recognise, and every image stoat supports except Alpine ships
	// bash, so bash stays the default for anything not in the registry.
	return "/bin/bash"
}

// extraPackages installs what the base block assumes is present but is not.
// The users: sudo key writes a sudoers fragment. Alpine ships no sudo
// binary (the cloud-init aport prefers doas); without this fragment,
// sudoers refers to a command that does not exist, and every escalating
// recipe fails.
//
// This returns its own #cloud-config-shaped fragment, not raw text spliced
// onto the base block. It becomes its own document in the
// cloud-config-archive (see buildArchive), next to the base users: block
// and any recipe bodies. A recipe's own packages: list then merges with
// this one instead of one silently overwriting the other.
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

// userData builds the seed's user-data: the hardware-proven users: block
// (parameterized by the guest's shell), the OS's own extra packages if it
// needs any, and every selected cloud recipe's body. mergeDocs folds them
// into one #cloud-config document. Nothing here looks for packages: or
// runcmd: by name, so a fragment using write_files: or any other key
// survives.
func userData(v *config.VM, pubkey string, recipeBodies []string) (string, error) {
	base := fmt.Sprintf(userDataTemplate, guestShell(v.OS), pubkey, consolePasswordBlock(v.ConsolePassword))

	docs := []string{base}
	if m := mountsDoc(v); m != "" {
		docs = append(docs, m)
	}
	if extra := extraPackages(v.OS); extra != "" {
		docs = append(docs, extra)
	}
	docs = append(docs, recipeBodies...)

	return mergeDocs(docs)
}

// SkipShares reports whether osName's guest.toml sets backend.cloudinit's
// skip_9p. A kernel without the 9p module makes cloud-init's mounts module
// fail the whole seed on every boot, and nofail does not cover an unknown
// filesystem type.
func SkipShares(osName string) bool {
	o, ok := guest.Lookup(osName)
	if !ok {
		return false
	}
	skip, _ := o.Backends["cloudinit"]["skip_9p"].(bool)
	return skip
}

// mountsDoc mounts the 9p exports. Cloud VMs used to get the exports on the
// QEMU command line with nothing to mount them, so the share silently did
// nothing.
//
// debian's cloud kernel (deb13-cloud) ships no 9p module, so the mount can
// never succeed. Its guest.toml sets backend.cloudinit's skip_9p, and this
// document is skipped there.
//
// nofail keeps a share that drops out at runtime from holding up boot. The
// host mount is ro, matching what QEMU enforces, so a write fails immediately
// instead of after a remount that appears to succeed.
func mountsDoc(v *config.VM) string {
	if SkipShares(v.OS) {
		return ""
	}
	const opts = "trans=virtio,version=9p2000.L,%s,_netdev,nofail"
	var b strings.Builder
	b.WriteString("#cloud-config\nmounts:\n")
	fmt.Fprintf(&b, "  - [ work, /mnt/work, 9p, %q, \"0\", \"0\" ]\n", fmt.Sprintf(opts, "rw"))
	if v.Share != "" {
		fmt.Fprintf(&b, "  - [ host, /mnt/host, 9p, %q, \"0\", \"0\" ]\n", fmt.Sprintf(opts, "ro"))
	}
	for _, s := range v.Shares {
		b.WriteString(fmt.Sprintf("  - [ %s, %s, 9p, %q, \"0\", \"0\" ]\n", s.Tag, s.Guest, fmt.Sprintf(opts, "rw")))
	}
	return b.String()
}

// consolePasswordBlock renders the two lines that make console login work,
// or nothing when no password is set.
//
// It uses plain_text_passwd, not a hash. cloud-init prefers a hash, and for
// an internet-facing server that is the right choice. Here the hash would
// sit in the same seed file, in the same data root, next to the private key
// that already grants full access to this VM. Hashing would guard against
// someone who can read the seed but not the key, a split that does not
// happen here, at the cost of a crypt(3) dependency or a hard openssl
// requirement at VM-create time.
func consolePasswordBlock(password string) string {
	if password == "" {
		return ""
	}
	return fmt.Sprintf("    lock_passwd: false\n    plain_text_passwd: %q\n", password)
}

const metaDataTemplate = `instance-id: %q
local-hostname: %q
`

// haveCloudInit reports whether the cloud-init binary is on PATH. Arch does
// not install cloud-init by default (see
// guest-subsystem.md §10). Schema validation must degrade to "not checked",
// not "assumed valid". Callers of ValidateFragment must treat a nil error
// with no annotated output as "not checked", never as "passed".
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
	defer func() { _ = os.Remove(f.Name()) }()

	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
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

// Seed writes <v.OvlDir()>/seed/{user-data,meta-data} and builds
// <v.OvlDir()>/seed.iso (ISO9660, volume label CIDATA) via xorriso. It
// returns the iso path. recipeBodies are the bodies of v's selected cloud
// recipes, already read by the caller. Their packages:/runcmd: sections
// merge into user-data alongside the fixed, hardware-proven users: block.
// cloud-init's packages: list only runs at first boot, so this is how a
// cloud VM's recipes get applied, unlike the ssh-provisioning path other
// backends use.
func Seed(v *config.VM, pubkey string, recipeBodies []string) (string, error) {
	seedDir := filepath.Join(v.OvlDir(), "seed")
	if err := os.MkdirAll(seedDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(seedDir, 0o700); err != nil {
		return "", err
	}

	ud, err := userData(v, pubkey, recipeBodies)
	if err != nil {
		return "", err
	}
	if err := writePrivateFile(filepath.Join(seedDir, "user-data"), []byte(ud)); err != nil {
		return "", err
	}

	metaData := fmt.Sprintf(metaDataTemplate, "stoat-"+v.Name, v.Name)
	if err := writePrivateFile(filepath.Join(seedDir, "meta-data"), []byte(metaData)); err != nil {
		return "", err
	}

	isoPath := filepath.Join(v.OvlDir(), "seed.iso")
	if err := writeSeedISO(isoPath, []seedFile{
		{name: "user-data", body: []byte(ud)},
		{name: "meta-data", body: []byte(metaData)},
	}); err != nil {
		return "", err
	}
	return isoPath, nil
}

type seedFile struct {
	name string
	body []byte
}

// seedISOSlack is the space ISO9660 needs beyond the file data: a 32 KiB
// system area, the volume descriptors, and a directory record per file in both
// the primary and the Joliet tree. The image keeps the size it is created
// with, so this is the whole cost of a seed on disk.
const seedISOSlack = 1 << 20

const isoBlockSize = 2048

// writeSeedISO writes files into an ISO9660 image labelled CIDATA, with Joliet
// and Rock Ridge. cloud-init's NoCloud datasource finds the seed by that label
// and reads the lowercase names, which plain ISO9660 cannot hold.
//
// The image is built beside its final path and renamed, because the writer
// demands a file that does not exist yet and creates it under the caller's
// umask. user-data carries the recipe bodies, so the image is never readable
// by another account: the enclosing directory is private, and the file is
// 0600 before it takes the seed's name.
func writeSeedISO(isoPath string, files []seedFile) error {
	size := int64(seedISOSlack)
	for _, f := range files {
		size += int64(len(f.body))
	}

	building := isoPath + ".building"
	if err := os.Remove(building); err != nil && !os.IsNotExist(err) {
		return err
	}
	// ISO9660 accepts a 2048-byte block and nothing smaller; the writer's
	// default of 512 is rejected outright.
	image, err := diskfs.Create(building, size, isoBlockSize)
	if err != nil {
		return fmt.Errorf("create seed image: %w", err)
	}
	defer os.Remove(building)
	if err := os.Chmod(building, 0o600); err != nil {
		return err
	}

	fs, err := image.CreateFilesystem(disk.FilesystemSpec{Partition: 0, FSType: filesystem.TypeISO9660})
	if err != nil {
		return fmt.Errorf("create seed filesystem: %w", err)
	}
	for _, f := range files {
		out, err := fs.OpenFile("/"+f.name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("seed %s: %w", f.name, err)
		}
		if _, err := out.Write(f.body); err != nil {
			return fmt.Errorf("seed %s: %w", f.name, err)
		}
	}
	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("seed filesystem is %T, want iso9660", fs)
	}
	// The writer copies the identifier into a zero-filled field, and ISO9660
	// pads that field with spaces. A label read back with trailing NULs is not
	// the label NoCloud looks for, so pad it here. The Joliet descriptor takes
	// the first 16 characters of the same string as UCS-2.
	label := fmt.Sprintf("%-32s", "CIDATA")
	if err := iso.Finalize(iso9660.FinalizeOptions{
		VolumeIdentifier: label,
		Joliet:           true,
		RockRidge:        true,
	}); err != nil {
		return fmt.Errorf("finalize seed image: %w", err)
	}
	if err := image.Close(); err != nil {
		return err
	}
	return os.Rename(building, isoPath)
}

func writePrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
