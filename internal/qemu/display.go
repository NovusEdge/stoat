package qemu

import (
	"fmt"
	"os/exec"
	"strings"
)

// Attach is how to open a headless VM's VNC display ON THIS HOST.
//
// It reports what is actually installed rather than a fixed suggestion,
// because a printed command reads as an instruction and a command naming a
// binary the user does not have fails as one. That is why this lives here,
// next to the code that binds the socket, rather than in a doc.
type Attach struct {
	// Command is ready to run as typed. Empty when none of Missing is
	// installed.
	Command string
	// Then is a second step Command does not do by itself, empty when there
	// is none. The socat bridge needs one: it only republishes the socket on
	// TCP, and a VNC client still has to connect to that.
	Then string
	// Missing names the viewers that were looked for. Set only when Command
	// is empty, so a caller can say what to install.
	Missing []string
}

// bridgePort is the loopback port the socat fallback publishes on. 5900 is
// VNC's own default, so a client pointed at 127.0.0.1:5900 needs no
// explanation. Nothing checks it is free first: that would be a race, and
// socat reports the collision itself.
const bridgePort = 5900

// vncViewers are the binaries AttachVNC knows how to drive, in preference
// order. Kept short on purpose: every entry is a claim that this exact
// invocation works against a unix-socket VNC server, and an untested claim
// printed as a command is worse than admitting nothing is installed.
var vncViewers = []string{"gvncviewer", "socat"}

// AttachVNC builds the command for the VNC server bound at sock.
func AttachVNC(sock string) Attach {
	q := shellQuote(sock)
	// gvncviewer takes a unix socket path as its argument directly. It is the
	// only viewer here confirmed to do so; the rest of the common ones speak
	// TCP only, which is what the socat bridge below exists for.
	if _, err := exec.LookPath("gvncviewer"); err == nil {
		return Attach{Command: "gvncviewer " + q}
	}
	// fork keeps socat listening after a viewer disconnects, so reattaching
	// to a long-lived desktop VM does not mean re-running this each time.
	if _, err := exec.LookPath("socat"); err == nil {
		return Attach{
			Command: fmt.Sprintf("socat TCP-LISTEN:%d,bind=127.0.0.1,reuseaddr,fork UNIX-CONNECT:%s", bridgePort, q),
			Then:    fmt.Sprintf("then point any VNC client at 127.0.0.1:%d", bridgePort),
		}
	}
	return Attach{Missing: append([]string(nil), vncViewers...)}
}

// shellQuote quotes a path only when it holds a character a shell would act
// on, so the common case stays copy-pasteable and a data root with a space in
// it still produces a command that runs.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`*?[]&|;<>(){}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
