package cli

import "testing"

// TestSplitCopyArgs pins the drive-letter carve-out: on Windows a single
// letter before ":\" or ":/" is a drive, never a VM name, since every
// absolute local path there starts with one and would otherwise read as
// remote on both sides. Linux gets no carve-out, so "C:something" there stays
// a VM name the same as before this change.
func TestSplitCopyArgs(t *testing.T) {
	cases := []struct {
		name         string
		src, dst     string
		goos         string
		wantVM       string
		wantRemote   string
		wantLocal    string
		wantToRemote bool
		wantErr      bool
	}{
		{
			name: "windows drive source, remote dest",
			src:  `C:\Users\me\file.txt`, dst: "dev:/tmp/x",
			goos:   "windows",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: `C:\Users\me\file.txt`, wantToRemote: true,
		},
		{
			name: "windows drive dest, remote source",
			src:  "dev:/tmp/x", dst: `C:\Users\me\file.txt`,
			goos:   "windows",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: `C:\Users\me\file.txt`, wantToRemote: false,
		},
		{
			name: "windows drive with forward slash",
			src:  `C:/Users/me/file.txt`, dst: "dev:/tmp/x",
			goos:   "windows",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: `C:/Users/me/file.txt`, wantToRemote: true,
		},
		{
			name: "linux absolute path, remote dest",
			src:  "/home/me/file.txt", dst: "dev:/tmp/x",
			goos:   "linux",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: "/home/me/file.txt", wantToRemote: true,
		},
		{
			name: "linux absolute path, remote source",
			src:  "dev:/tmp/x", dst: "/home/me/file.txt",
			goos:   "linux",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: "/home/me/file.txt", wantToRemote: false,
		},
		{
			name: "relative path, remote dest",
			src:  "build.sh", dst: "dev:/tmp/x",
			goos:   "linux",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: "build.sh", wantToRemote: true,
		},
		{
			name: "relative path, remote source",
			src:  "dev:/tmp/x", dst: "build.sh",
			goos:   "linux",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: "build.sh", wantToRemote: false,
		},
		{
			// A VM named with one letter is legal today and stays legal: the
			// carve-out only fires when the other side looks like a drive
			// path (a "\" or "/" right after the colon). A drive-relative
			// remote path ("c:foo", no separator after the colon) is
			// indistinguishable from a one-letter VM name, so it still reads
			// as one.
			name: "one-letter vm name still works on windows",
			src:  "c:foo", dst: "/local/file.txt",
			goos:   "windows",
			wantVM: "c", wantRemote: "foo", wantLocal: "/local/file.txt", wantToRemote: false,
		},
		{
			// No carve-out off Windows: "C:something" is a legal Linux
			// filename and must parse exactly as it did before this change.
			name: "colon-prefixed filename on linux is unaffected",
			src:  "C:something", dst: "./app.log",
			goos:   "linux",
			wantVM: "C", wantRemote: "something", wantLocal: "./app.log", wantToRemote: false,
		},
		{
			name: "unc path, remote dest",
			src:  `\\server\share\file.txt`, dst: "dev:/tmp/x",
			goos:   "windows",
			wantVM: "dev", wantRemote: "/tmp/x", wantLocal: `\\server\share\file.txt`, wantToRemote: true,
		},
		{
			name: "both sides remote is an error",
			src:  "dev:/tmp/x", dst: "other:/tmp/y",
			goos:    "linux",
			wantErr: true,
		},
		{
			name: "neither side remote is an error",
			src:  "/tmp/x", dst: "/tmp/y",
			goos:    "linux",
			wantErr: true,
		},
		{
			name: "two windows drive paths is an error",
			src:  `C:\tmp\x`, dst: `D:\tmp\y`,
			goos:    "windows",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm, remote, local, toRemote, err := splitCopyArgs(tc.src, tc.dst, tc.goos)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitCopyArgs(%q, %q, %q) = nil error, want one", tc.src, tc.dst, tc.goos)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitCopyArgs(%q, %q, %q) unexpected error: %v", tc.src, tc.dst, tc.goos, err)
			}
			if vm != tc.wantVM || remote != tc.wantRemote || local != tc.wantLocal || toRemote != tc.wantToRemote {
				t.Fatalf("splitCopyArgs(%q, %q, %q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
					tc.src, tc.dst, tc.goos, vm, remote, local, toRemote,
					tc.wantVM, tc.wantRemote, tc.wantLocal, tc.wantToRemote)
			}
		})
	}
}
