package apkovl

import (
	"fmt"

	"github.com/novusedge/stoat/internal/config"
)

// GenerateAnswerfile creates an Alpine setup-alpine answerfile from VM
// config, for unattended disk installs (setup-alpine -f <answerfile>).
func GenerateAnswerfile(v *config.VM) string {
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
NTPOPTS="-c chrony"
DISKOPTS="-m sys /dev/vda"
LBUOPTS="none"
APKCACHEOPTS="/var/cache/apk"
`, v.Name)
}
