package hostops

import (
	"strings"
	"testing"
)

// TestMessage exercises darwin/arm64 and windows/amd64 wording on whatever
// GOOS this build runs on, since Message takes goos/goarch as arguments
// instead of reading runtime.GOOS. CI only builds host-primitives natively
// on macos-14 and windows-2025; this table proves the text on Linux too.
func TestMessage(t *testing.T) {
	cases := []struct {
		name           string
		goos, goarch   string
		wantSubstrings []string
	}{
		{
			name:   "darwin arm64",
			goos:   "darwin",
			goarch: "arm64",
			wantSubstrings: []string{
				"darwin/arm64",
				"QEMU HVF accelerator",
				"stoat#82",
				"doctor and capabilities still work",
				"Linux with KVM is the supported configuration today",
			},
		},
		{
			name:   "windows amd64",
			goos:   "windows",
			goarch: "amd64",
			wantSubstrings: []string{
				"windows/amd64",
				"QEMU WHPX accelerator",
				"stoat#83",
				"doctor and capabilities still work",
				"Linux with KVM is the supported configuration today",
			},
		},
		{
			name:   "unlisted host falls back without inventing a requirement",
			goos:   "freebsd",
			goarch: "amd64",
			wantSubstrings: []string{
				"freebsd/amd64",
				"has no qualified runtime yet",
				"Linux with KVM is the supported configuration today",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := Message(tc.goos, tc.goarch)
			for _, want := range tc.wantSubstrings {
				if !strings.Contains(msg, want) {
					t.Errorf("Message(%q, %q) = %q, want substring %q", tc.goos, tc.goarch, msg, want)
				}
			}
		})
	}
}
