package wire

import "github.com/novusedge/stoat/internal/core"

// sampleVM is a fully-populated core.VM, deliberately including
// ConsolePassword and Paths (both real absolute-looking values) so the
// "never serialized" tests have something to catch.
func sampleVM() core.VM {
	return core.VM{
		Name:            "work",
		OS:              "alpine",
		Mode:            "live",
		Backend:         "apkovl",
		State:           core.StateRunning,
		RAM:             4096,
		CPUs:            4,
		Disk:            "8G",
		Share:           "/home/u/src",
		Recipes:         []string{"xfce"},
		SSHPort:         2222,
		SSHUser:         "root",
		ISO:             "isos/alpine-virt.iso",
		Base:            "",
		ConsolePassword: "hunter2password",
		Forwards:        []core.PortForward{{HostPort: 8080, GuestPort: 80}},
		Installed:       false,
		AllowExec:       true,
		Paths: core.Paths{
			Dir:           "/home/u/.stoat/work",
			Disk:          "/home/u/.stoat/work/disk.qcow2",
			ConsoleLog:    "/home/u/.stoat/work/console.log",
			ApplyLog:      "/home/u/.stoat/work/last-provision.log",
			VNCSocket:     "/home/u/.stoat/work/vnc.sock",
			MonitorSocket: "/home/u/.stoat/work/monitor.sock",
		},
	}
}

// execResultWith returns an ExecResult carrying s as both stdout and
// stderr, for tests that only care about one field's encoding behaviour.
func execResultWith(s string) core.ExecResult {
	return core.ExecResult{Stdout: s, Stderr: s, ExitCode: 0}
}
