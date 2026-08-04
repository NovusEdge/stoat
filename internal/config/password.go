package config

import (
	"crypto/rand"
	"encoding/hex"
)

// DefaultConsolePassword is what a new cloud VM gets unless a random one is
// asked for. A fixed, documented value is the point: you are looking at a
// login prompt at the VM's VNC console on your own machine (a cloud VM never
// gets a qemu window, see qemu.NeedsWindow), usually because ssh isn't working,
// and having to go and look something up in that moment is the failure this
// is meant to prevent.
//
// It is safe here in a way it would not be on a server: the seed sets
// ssh_pwauth: false, so this password is refused over the forwarded port and
// only works at the graphical console. Anyone who can reach that console can
// already read ~/.stoat, which holds the private key that grants full access
// to the same VM.
const DefaultConsolePassword = "stoat"

// RandomConsolePassword returns 32 hex characters from crypto/rand: the
// equivalent of `openssl rand -hex 16`. For when a VM's console shouldn't
// open to a value that is written down in this repository.
func RandomConsolePassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
