package apkovl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/novusedge/stoat/internal/config"
)

// KernelAppend is the kernel command line for an Alpine live or installer boot,
// taken from the ISO's syslinux entry. It is the same across the x86_64 Alpine
// flavors, so it is fixed here rather than parsed.
const KernelAppend = "modules=loop,squashfs,sd-mod,usb-storage quiet"

// KernelPath and InitramfsPath are where ExtractKernel writes the boot files,
// under the VM directory with fixed names so Args needs no lookup.
func KernelPath(v *config.VM) string    { return filepath.Join(v.Dir, "vmlinuz") }
func InitramfsPath(v *config.VM) string { return filepath.Join(v.Dir, "initramfs") }

// ExtractKernel copies the kernel and initramfs out of the boot ISO so QEMU can
// boot them directly with -kernel/-initrd, bypassing the ISO's ISOLINUX.
//
// ISOLINUX shows a boot: prompt whose TIMEOUT never counts down under QEMU's
// BIOS timer, so a syslinux boot hangs until a key is pressed and an unattended
// install never starts. Booting the kernel directly removes the prompt. The ISO
// stays attached as a cdrom, so the Alpine initramfs still finds modloop on it.
//
// The source names come from the ISO itself (vmlinuz-lts on standard,
// vmlinuz-virt on virt), so any Alpine flavor works. The files are cached in
// the VM directory and re-extracted only when missing.
func ExtractKernel(v *config.VM) error {
	if fileExists(KernelPath(v)) && fileExists(InitramfsPath(v)) {
		return nil
	}
	iso := v.ISOPath()
	kSrc, err := isoFind(iso, "vmlinuz-*")
	if err != nil {
		return fmt.Errorf("locating kernel in %s: %w", iso, err)
	}
	iSrc, err := isoFind(iso, "initramfs-*")
	if err != nil {
		return fmt.Errorf("locating initramfs in %s: %w", iso, err)
	}
	if err := isoExtract(iso, kSrc, KernelPath(v)); err != nil {
		return err
	}
	return isoExtract(iso, iSrc, InitramfsPath(v))
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Size() > 0
}

// isoFind returns the single /boot entry in iso matching glob (e.g.
// "vmlinuz-*"). More than one match, or none, is an error: the caller needs
// exactly one kernel to boot.
func isoFind(iso, glob string) (string, error) {
	out, err := exec.Command("xorriso", "-indev", iso, "-find", "/boot", "-name", glob).Output()
	if err != nil {
		return "", fmt.Errorf("xorriso -find: %w", err)
	}
	var hits []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.Trim(strings.TrimSpace(line), "'")
		if strings.HasPrefix(line, "/boot/") {
			hits = append(hits, line)
		}
	}
	if len(hits) != 1 {
		return "", fmt.Errorf("want exactly one /boot/%s in the iso, found %d", glob, len(hits))
	}
	return hits[0], nil
}

// isoExtract copies one file out of the iso to dst.
func isoExtract(iso, src, dst string) error {
	out, err := exec.Command("xorriso", "-osirrox", "on", "-indev", iso, "-extract", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("xorriso -extract %s: %s", src, strings.TrimSpace(string(out)))
	}
	if !fileExists(dst) {
		return fmt.Errorf("xorriso -extract %s produced no file at %s", src, dst)
	}
	return nil
}
