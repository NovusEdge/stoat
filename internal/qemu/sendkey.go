package qemu

import (
	"fmt"
	"net"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

// dialMonitor opens v's qemu monitor socket. Stop and TypeConsolePassword
// both talk to the monitor; this is the one place that knows how to reach
// it, so a change to the transport (or a future switch to QMP) is one edit.
func dialMonitor(v *config.VM) (net.Conn, error) {
	return net.Dial("unix", v.MonitorPath())
}

// consoleKeyDelay is the pause between keystrokes sent over the monitor. The
// guest needs time to notice and process each one; sending them back to back
// risks the console dropping keys the way a human mashing a keyboard too
// fast would.
const consoleKeyDelay = 50 * time.Millisecond

// passwordKeys turns password into the sequence of qemu monitor "sendkey"
// key names needed to type it, one call per character.
//
// The monitor's sendkey command takes KEY NAMES, not characters: digits and
// lowercase letters are their own bare name (sendkey a, sendkey 1, ...), but
// anything needing shift (uppercase) or not guaranteed to be a plain key
// (punctuation, whitespace, unicode) is not — and the console password is
// user-settable, so it can contain any of those. Rather than guess at a
// shift/dead-key mapping and risk typing the wrong thing into a login
// prompt, this refuses outright the moment it sees a character outside the
// allowlist: silently sending a partial or wrong password is worse than
// doing nothing.
func passwordKeys(password string) ([]string, error) {
	keys := make([]string, 0, len(password))
	for _, r := range password {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')) {
			return nil, fmt.Errorf(
				"console password contains %q, which can't be typed reliably over the qemu monitor — enter it manually at the console",
				string(r))
		}
		keys = append(keys, string(r))
	}
	return keys, nil
}

// TypeConsolePassword has qemu type v's console password into the guest via
// the monitor's sendkey command, so nobody has to hand-retype a 32-character
// hex string at a login prompt with no host-guest clipboard (the console is
// a GTK window, not a terminal). Refuses rather than sends anything if the
// password contains a character it cannot type correctly, or if there is no
// password to send.
func TypeConsolePassword(v *config.VM) error {
	if v.ConsolePassword == "" {
		return fmt.Errorf("%s has no console password set", v.Name)
	}
	keys, err := passwordKeys(v.ConsolePassword)
	if err != nil {
		return err
	}
	c, err := dialMonitor(v)
	if err != nil {
		return fmt.Errorf("qemu monitor: %w", err)
	}
	defer c.Close()
	for _, k := range keys {
		if _, err := fmt.Fprintln(c, "sendkey "+k); err != nil {
			return fmt.Errorf("qemu monitor: %w", err)
		}
		time.Sleep(consoleKeyDelay)
	}
	return nil
}
