package apkovl

import (
	"fmt"

	"github.com/novusedge/stoat/internal/config"
)

// GenerateAnswerfile creates an Alpine setup-alpine answerfile from VM config,
// for unattended disk installs (setup-alpine -e -f <answerfile>). rootSSHKey is
// the stoat public key line.
//
// Every stage setup-alpine would prompt for is answered here, or the run stalls
// with no one to answer it (verified against alpine-conf's setup-alpine,
// setup-user, setup-sshd and setup-disk):
//   - -e (command line) empties the root password, skipping its prompt. There
//     is no ROOTPASSWD answerfile variable. stoat reaches the guest over an ssh
//     key, so an empty root password costs nothing.
//   - USEROPTS="none" makes setup-user exit without creating a user, so the
//     "Setup a user?" prompt never fires. Root stays the only account. Leave
//     USERSSHKEY unset, or setup-user stops taking the none early-exit.
//   - ROOTSSHKEY makes setup-sshd skip its root-key prompt. The key is already
//     in /root/.ssh/authorized_keys via the overlay, and setup-disk -m sys
//     copies it to the installed disk; this line only suppresses the prompt.
//   - ERASE_DISKS matching DISKOPTS's disk skips setup-disk's erase
//     confirmation. -m sys also skips the disk-mode and lbu/apkcache prompts.
func GenerateAnswerfile(v *config.VM, rootSSHKey string) string {
	return fmt.Sprintf(`KEYMAPOPTS="us us"
HOSTNAMEOPTS="-n %s"
INTERFACESOPTS="auto lo
iface lo inet loopback

auto eth0
iface eth0 inet dhcp
"
DNSOPTS="-d local"
TIMEZONEOPTS="-z UTC"
PROXYOPTS="none"
APKREPOSOPTS="-1"
SSHDOPTS="-c openssh"
ROOTSSHKEY="%s"
USEROPTS="none"
NTPOPTS="-c chrony"
DISKOPTS="-m sys /dev/vda"
ERASE_DISKS="/dev/vda"
LBUOPTS="none"
APKCACHEOPTS="/var/cache/apk"
`, v.Name, rootSSHKey)
}
