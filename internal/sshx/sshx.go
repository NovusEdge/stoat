// Package sshx runs commands inside guests over the forwarded SSH port.
package sshx

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/recipes"
)

// WaitTimeout is how long Provision waits for sshd after a start. A first
// live boot plus dhcp is slower than a warm one, so this is generous.
const WaitTimeout = 90 * time.Second

// Args returns the argv (excluding argv[0]) for ssh into v. Host key checks
// are off on purpose: this is a loopback forward to a VM stoat just built,
// and live VMs are recreated constantly.
func Args(v *config.VM, extra ...string) []string {
	a := []string{
		"-p", fmt.Sprint(v.SSHPort),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=5",
		"-i", keys.PrivatePath(),
		"root@127.0.0.1",
	}
	return append(a, extra...)
}

// Wait blocks until the forwarded port accepts a connection, or timeout.
func Wait(v *config.VM, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := fmt.Sprintf("127.0.0.1:%d", v.SSHPort)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s: ssh not reachable on port %d after %s", v.Name, v.SSHPort, timeout)
}

// Provision runs each of v's recipes over ssh, streaming output to
// last-provision.log. The detail view tails that file on a ticker, so there
// is no channel plumbing between this and the UI.
func Provision(v *config.VM) error {
	log, err := os.Create(v.Dir + "/last-provision.log")
	if err != nil {
		return err
	}
	defer log.Close()

	fmt.Fprintf(log, "waiting for ssh on port %d…\n", v.SSHPort)
	if err := Wait(v, WaitTimeout); err != nil {
		fmt.Fprintf(log, "FAILED: %v\n", err)
		return err
	}

	for _, name := range v.Recipes {
		body, err := recipes.Read(name)
		if err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return err
		}
		fmt.Fprintf(log, "\n=== recipe %s ===\n", name)

		cmd := exec.Command("ssh", Args(v, "sh", "-s")...)
		cmd.Stdin = strings.NewReader(body)
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(log, "FAILED: recipe %s: %v\n", name, err)
			return fmt.Errorf("recipe %s: %w", name, err)
		}
	}
	fmt.Fprintln(log, "\ndone")
	return nil
}
