package tui

import (
	"os/exec"
	"strings"
	"testing"
)

// Shapes below are trimmed from real `cloud-init status --format json`
// output; only the keys decodeCloudInitStatus reads are kept plus enough of
// the rest to prove extra keys don't break decoding.
func TestDecodeCloudInitStatus(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "done, no errors",
			in: `{
				"datasource": "DataSourceNoCloud",
				"status": "done",
				"extended_status": "done",
				"errors": [],
				"recoverable_errors": {}
			}`,
			want: "done",
		},
		{
			name: "alpine: degraded extended_status but empty errors is still done",
			in: `{
				"status": "done",
				"extended_status": "degraded done",
				"errors": [],
				"recoverable_errors": {
					"WARNING": ["Failed to run module keys_to_console (main/config-modules/config-05_keys-to-console.py): FileNotFoundError: [Errno 2] No such file or directory: '/usr/lib/cloud-init/write-ssh-key-fingerprints'"]
				}
			}`,
			want: "done",
		},
		{
			name: "running",
			in:   `{"status": "running", "extended_status": "running", "errors": []}`,
			want: "running",
		},
		{
			name: "populated errors list is error regardless of status",
			in: `{
				"status": "error",
				"extended_status": "error",
				"errors": ["Failed to run module write_files (main/cloud_init_modules.py): PermissionError"]
			}`,
			want: "error",
		},
		{
			name: "errors present even though status string says done",
			in:   `{"status": "done", "extended_status": "done", "errors": ["something recoverable_errors missed"]}`,
			want: "error",
		},
		{
			name: "disabled",
			in:   `{"status": "disabled", "extended_status": "disabled", "errors": []}`,
			want: "disabled",
		},
		{
			name: "not run",
			in:   `{"status": "not run", "extended_status": "not run", "errors": []}`,
			want: "not-run",
		},
		{
			name: "garbage input is unknown, not a crash",
			in:   `usage: cloud-init status [-h] [--long] [--wait] [--format {json,tabular}]`,
			want: "unknown",
		},
		{
			name: "empty input is unknown",
			in:   ``,
			want: "unknown",
		},
		{
			name: "valid json but not the expected shape is unknown",
			in:   `[1, 2, 3]`,
			want: "unknown",
		},
		{
			name: "unrecognized status word is unknown",
			in:   `{"status": "some-future-state", "extended_status": "some-future-state", "errors": []}`,
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeCloudInitStatus([]byte(tt.in))
			if got != tt.want {
				t.Errorf("decodeCloudInitStatus(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestCloudInitBarSuppressedOnTerminalFailure pins the fix for a full bar
// rendering next to a red "failed" label. "error", "disabled" and "not-run"
// are terminal states that never reach 100% install; cloudInitBar must draw
// nothing for them. "running" still gets a bar, so the suppression is
// specific to the non-success terminal states, not to every status.
func TestCloudInitBarSuppressedOnTerminalFailure(t *testing.T) {
	p := newCloudInitProgress()

	for _, status := range []string{"error", "disabled", "not-run"} {
		if got := cloudInitBar(p, status); got != "" {
			t.Errorf("cloudInitBar(%q) = %q, want no bar", status, got)
		}
	}

	if got := cloudInitBar(p, "running"); !strings.Contains(got, "█") {
		t.Errorf("cloudInitBar(%q) = %q, want a bar", "running", got)
	}
}

// TestOutputIgnoresStderrNoise exercises the mechanism checkCloudInit relies
// on: Cmd.Output(), not Cmd.CombinedOutput(). A login banner or stray
// warning an ssh session writes to stderr never reaches the decoder. An ssh
// MOTD ahead of the JSON in a combined stream would break json.Unmarshal.
func TestOutputIgnoresStderrNoise(t *testing.T) {
	script := `echo "Welcome to Alpine Linux, this line is not JSON" >&2
echo '{"status": "done", "extended_status": "done", "errors": []}'
`
	out, err := exec.Command("sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("running test script: %v", err)
	}
	got := decodeCloudInitStatus(out)
	if got != "done" {
		t.Errorf("decodeCloudInitStatus(stdout-only output) = %q, want %q (stderr noise: %q)", got, "done", out)
	}
}
