package sshx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

func TestCloudInitWaitUsesUnprivilegedSSHThenEscalatesSetup(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		exitCode int
	}{
		{
			name:     "done",
			status:   `{"status":"done","extended_status":"done","errors":[],"recoverable_errors":{}}`,
			exitCode: 0,
		},
		{
			name: "degraded exit two is recoverable",
			status: `{"status":"done","extended_status":"degraded done","errors":[],` +
				`"recoverable_errors":{"WARNING":["keys_to_console helper is unavailable"]},"boot_status_code":2}`,
			exitCode: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("STOAT_HOME", root)
			capture := installCloudInitSSH(t, tc.status, tc.exitCode)
			port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")
			vmDir := filepath.Join(root, "work")
			if err := os.MkdirAll(vmDir, 0o755); err != nil {
				t.Fatal(err)
			}
			v := &config.VM{
				Name: "work", Dir: vmDir, OS: "alpine", Backend: "cloudinit",
				SSHUser: "stoat", SSHPort: port,
			}

			if err := Provision(context.Background(), v); err != nil {
				t.Fatalf("Provision() = %v, want success for %s cloud-init status", err, tc.name)
			}

			calls := readCloudInitSSHCalls(t, capture)
			if len(calls) < 2 {
				t.Fatalf("ssh calls = %v, want cloud-init wait followed by package setup", calls)
			}
			waitCall := calls[0]
			if !strings.Contains(waitCall, "stoat@127.0.0.1") {
				t.Errorf("cloud-init wait did not use the seeded SSH account: %q", waitCall)
			}
			if strings.Contains(waitCall, "sudo") {
				t.Errorf("cloud-init wait escalated before the seeded user had sudo: %q", waitCall)
			}
			if !strings.Contains(waitCall, "cloud-init status --wait --format json") {
				t.Errorf("cloud-init wait argv = %q, want direct JSON status --wait", waitCall)
			}
			if !strings.Contains(calls[1], "sudo -n") {
				t.Errorf("package setup did not escalate after cloud-init completed: %q", calls[1])
			}
			log, err := os.ReadFile(v.ProvisionLogPath())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(log), "\ndone") {
				t.Fatalf("provision log does not report completion:\n%s", log)
			}
		})
	}
}

func TestCloudInitReadinessRefusesBeforeFurtherProvisioning(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		exitCode int
	}{
		{
			name:     "hard error",
			status:   `{"status":"error","extended_status":"error","errors":["write_files failed"]}`,
			exitCode: 1,
		},
		{
			name:     "non terminal status",
			status:   `{"status":"running","extended_status":"running","errors":[]}`,
			exitCode: 0,
		},
		{
			name:     "ssh transport failure",
			exitCode: 255,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("STOAT_HOME", root)
			writeCloudReadinessRecipe(t, root)
			capture := installCloudInitSSH(t, tc.status, tc.exitCode)
			port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")
			vmDir := filepath.Join(root, "work")
			if err := os.MkdirAll(vmDir, 0o755); err != nil {
				t.Fatal(err)
			}
			v := &config.VM{
				Name: "work", Dir: vmDir, OS: "alpine", Backend: "cloudinit",
				SSHUser: "stoat", SSHPort: port, Recipes: []string{"probe"},
			}

			if err := Provision(context.Background(), v); err == nil {
				t.Fatalf("Provision() succeeded for %s cloud-init status", tc.name)
			}
			calls := readCloudInitSSHCalls(t, capture)
			if len(calls) != 1 {
				t.Fatalf("ssh calls = %v, want only the cloud-init readiness call", calls)
			}
			if strings.Contains(calls[0], "sudo") {
				t.Errorf("cloud-init readiness call escalated: %q", calls[0])
			}
			log, err := os.ReadFile(v.ProvisionLogPath())
			if err != nil {
				t.Fatal(err)
			}
			logText := string(log)
			if strings.Contains(logText, "refreshing the package index") {
				t.Errorf("package setup ran after %s cloud-init status:\n%s", tc.name, logText)
			}
			if strings.Contains(logText, RecipeMarker("probe")) {
				t.Errorf("recipe ran after %s cloud-init status:\n%s", tc.name, logText)
			}
			if strings.Contains(logText, "\ndone") {
				t.Errorf("refused provisioning reported success after %s cloud-init status:\n%s", tc.name, logText)
			}
		})
	}
}

func TestCloudInitWaitCancellationKeepsContextErrorAndStopsChild(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	pidFile := filepath.Join(root, "cloud-init.pid")
	capture := installBlockingCloudInitSSH(t, pidFile)
	port := acceptOnly(t, "SSH-2.0-OpenSSH_9.6\r\n")
	vmDir := filepath.Join(root, "work")
	if err := os.MkdirAll(vmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Name: "work", Dir: vmDir, OS: "alpine", Backend: "cloudinit",
		SSHUser: "stoat", SSHPort: port,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Provision(ctx, v) }()
	waitForFile(t, pidFile, 3*time.Second)
	cancel()

	var err error
	select {
	case err = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("Provision did not return after cloud-init wait cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Provision() = %v, want context.Canceled", err)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Errorf("cloud-init ssh process (pid %d) is still running after cancellation", pid)
	}
	log, err := os.ReadFile(v.ProvisionLogPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "CANCELLED") {
		t.Errorf("provision log does not record cancellation:\n%s", log)
	}
	if len(readCloudInitSSHCalls(t, capture)) != 1 {
		t.Errorf("ssh calls = %v, want only the cancelled cloud-init call", readCloudInitSSHCalls(t, capture))
	}
}

func installCloudInitSSH(t *testing.T, status string, exitCode int) string {
	t.Helper()
	bin := t.TempDir()
	capture := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + cloudShellQuote(capture) + "\n" +
		"case \"$*\" in\n" +
		"  *\"cloud-init status\"*)\n" +
		"    printf '%s' " + cloudShellQuote(status) + "\n" +
		"    exit " + strconv.Itoa(exitCode) + "\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return capture
}

func installBlockingCloudInitSSH(t *testing.T, pidFile string) string {
	t.Helper()
	bin := t.TempDir()
	capture := filepath.Join(bin, "calls.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + cloudShellQuote(capture) + "\n" +
		"case \"$*\" in\n" +
		"  *\"cloud-init status\"*)\n" +
		"    printf '%s\\n' \"$$\" > " + cloudShellQuote(pidFile) + "\n" +
		"    trap 'exit 143' TERM\n" +
		"    while :; do sleep 1; done\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return capture
}

func writeCloudReadinessRecipe(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "recipes", "probe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"probe\"\nscript = \"install.sh\"\n"
	if err := os.WriteFile(filepath.Join(dir, "recipe.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("echo probe\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readCloudInitSSHCalls(t *testing.T, capture string) []string {
	t.Helper()
	b, err := os.ReadFile(capture)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func cloudShellQuote(s string) string {
	return fmt.Sprintf("'%s'", strings.ReplaceAll(s, "'", `'\''`))
}
