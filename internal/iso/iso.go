// Package iso finds and downloads Alpine images into the stoat data root.
package iso

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
)

const (
	mirror   = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86_64/"
	indexURL = mirror + "latest-releases.yaml"
)

// Release is a concrete, resolved download: a filename with a known (or
// deliberately absent) checksum. It is produced either by Latest() (Alpine's
// published index) or by Resolve() (a direct-URL catalog Entry), and is what
// Download() actually fetches.
type Release struct {
	Flavor  string `yaml:"flavor"`
	File    string `yaml:"file"`
	Version string `yaml:"version"`

	// SHA256 holds the expected digest, despite the name: fetchChecksum may
	// set a SHA-512 digest here (Debian publishes only SHA512SUMS). Download
	// picks the algorithm by this string's hex length, 64 for sha256 or 128
	// for sha512, not by the field name. Empty means no checksum was found.
	SHA256 string `yaml:"sha256"`

	// URL, when set, is the full URL Download fetches. Latest() (Alpine)
	// leaves it empty; Download then resolves against downloadMirror+File.
	// Resolve() sets it for direct-URL catalog entries (cloud images),
	// whose files do not live under the Alpine mirror.
	URL string

	// Verified reports whether Download checked the downloaded bytes
	// against a published digest. Download sets it true only after a
	// byte-for-byte match, including the existing-file reuse shortcut. A
	// configured ChecksumURL alone does not set it. When SHA256 is empty,
	// Download fetches the file anyway and Verified stays false, so a
	// caller can tell an unverified download from a verified one.
	Verified bool
}

// Entry describes one image in stoat's catalog: what it is, where to fetch
// it, and which provisioning backend applies once it boots.
type Entry struct {
	ID      string
	OS      string
	Backend string // "apkovl" | "cloudinit" | "ssh"
	URL     string
	// Flavor selects a line in Alpine's latest-releases.yaml (e.g.
	// "alpine-standard", "alpine-virt"). It applies only when
	// OS == "alpine"; every other entry is a direct URL and Resolve
	// ignores Flavor for those.
	Flavor string
	// Variant distinguishes entries that share an OS: a version
	// ("24.04 LTS"), a release ("13 (trixie)"), or a build ("standard" vs
	// "virt"). It is a field, not derived from ID, because ID-stripping
	// fails for two entries: fedora-cloud and arch-cloud would both reduce
	// to "cloud".
	Variant string
	// Size is the download's approximate size, for display only. Catalog()
	// promises no network access, so Size is declared rather than fetched
	// by a HEAD request. It drifts as images are rebuilt in place. Nothing
	// may allocate, preallocate, or verify against it: the real size comes
	// from Content-Length at download time, and the real check is the
	// checksum.
	Size        int64
	ChecksumURL string
	SSHUser     string
	Notes       string
}

// mib is a readable way to write the declared sizes above.
const mib = 1 << 20

// OSGroup is one OS and every catalog entry belonging to it, in catalog
// order.
type OSGroup struct {
	OS      string
	Entries []Entry
}

// ByOS groups the catalog for a two-level picker: an OS, then a variant
// within it. Both levels keep the catalog's hand-written order, not sorted
// order, because the catalog lists the most generally useful image first
// and alphabetical order would lose that.
//
// Catalog() stays the API the rest of the code uses. ByOS is a display view
// of it.
func ByOS() []OSGroup {
	var groups []OSGroup
	at := map[string]int{} // OS -> index in groups
	for _, e := range Catalog() {
		i, seen := at[e.OS]
		if !seen {
			at[e.OS] = len(groups)
			groups = append(groups, OSGroup{OS: e.OS})
			i = len(groups) - 1
		}
		groups[i].Entries = append(groups[i].Entries, e)
	}
	return groups
}

// Catalog returns stoat's curated set of known-good images. It is an
// embedded slice, not fetched, so listing or picking from it needs no
// network access.
//
// ChecksumURL points at a published sums file, parsed at download time.
// Never a frozen hash: cloud images roll forward in place. Where a distro
// has no stable, algorithm-matching sums URL, ChecksumURL is left empty and
// Download() fetches without verification instead of guessing a URL.
func Catalog() []Entry {
	return []Entry{
		{
			ID:          "ubuntu-24.04",
			OS:          "ubuntu",
			Variant:     "24.04 LTS",
			Size:        595 * mib,
			Backend:     "cloudinit",
			URL:         "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img",
			ChecksumURL: "https://cloud-images.ubuntu.com/releases/24.04/release/SHA256SUMS",
			// The cloud image's distro-default user is "ubuntu", but
			// stoat's cloud-init seed (internal/cloudinit) creates and
			// keys only a "stoat" user, so that's what connects.
			SSHUser: "stoat",
			Notes:   "Ubuntu 24.04 LTS server cloud image",
		},
		{
			ID:      "debian-13",
			OS:      "debian",
			Variant: "13 (trixie)",
			Size:    328 * mib,
			Backend: "cloudinit",
			URL:     "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2",
			// Debian publishes SHA512SUMS, not SHA256, in 128-hex "<hex>
			// <filename>" (GNU coreutils) format. parseChecksum/Download
			// pick sha256 or sha512 by digest length (64 or 128 hex), so
			// this verifies correctly.
			ChecksumURL: "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS",
			SSHUser:     "stoat",
			Notes:       "Debian 13 (trixie) generic cloud image",
		},
		{
			ID:      "fedora-cloud",
			OS:      "fedora",
			Variant: "44",
			Size:    557 * mib,
			// Fedora keeps roughly N/N-1/N-2 under releases/, with no
			// latest/ symlink for cloud images. This entry needs a manual
			// bump every Fedora cycle and breaks once the pinned release
			// ages out of releases/ (moves to archives.fedoraproject.org,
			// filename changes too).
			Backend: "cloudinit",
			URL:     "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
			// Fedora's per-release CHECKSUM uses BSD format ("SHA256
			// (filename) = hex") inside a clearsigned PGP block, not the
			// GNU "<hex>  <filename>" form. parseChecksum has a BSD branch
			// for this; the PGP armor lines match neither format and are
			// skipped. The compose suffix ("1.7" here) is baked into both
			// this URL and ChecksumURL and changes per respin. Update both
			// together on the next bump.
			ChecksumURL: "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-44-1.7-x86_64-CHECKSUM",
			SSHUser:     "stoat",
			Notes:       "Fedora Cloud Base qcow2",
		},
		{
			ID:      "arch-cloud",
			OS:      "arch",
			Variant: "rolling",
			Size:    530 * mib,
			Backend: "cloudinit",
			URL:     "https://geo.mirror.pkgbuild.com/images/latest/Arch-Linux-x86_64-cloudimg.qcow2",
			// This mirror publishes a per-image ".SHA256" sidecar
			// ("<64hex>  <filename>"), the same format parseChecksum
			// already handles.
			ChecksumURL: "https://geo.mirror.pkgbuild.com/images/latest/Arch-Linux-x86_64-cloudimg.qcow2.SHA256",
			SSHUser:     "stoat",
			Notes:       "Arch Linux rolling cloud image",
		},
		{
			ID:      "alpine-standard",
			OS:      "alpine",
			Backend: "apkovl",
			URL:     indexURL,
			Flavor:  "alpine-standard",
			Variant: "standard",
			Size:    352 * mib,
			// Alpine embeds its own sha256 inline in each release entry
			// of latest-releases.yaml; Resolve() reads it via Latest().
			// ChecksumURL stays empty: verification happens through the
			// index instead, not because this entry is unverified.
			ChecksumURL: "",
			SSHUser:     "root",
			Notes:       "Alpine standard live ISO (existing apkovl path)",
		},
		{
			ID:      "alpine-virt",
			OS:      "alpine",
			Backend: "apkovl",
			URL:     indexURL,
			Flavor:  "alpine-virt",
			Variant: "virt",
			Size:    66 * mib,
			// Same index, different flavor line (see alpine-standard
			// above). Virt targets VM guests only: virtio drivers, no
			// baremetal support, meaningfully smaller. It is offered
			// alongside standard, not in place of it, since QEMU is
			// stoat's only target.
			ChecksumURL: "",
			SSHUser:     "root",
			Notes:       "Alpine virt live ISO (smaller, virtio-only, built for VM guests)",
		},
		{
			ID:      "alpine-cloud",
			OS:      "alpine",
			Variant: "3.24 cloud",
			Size:    175 * mib,
			Backend: "cloudinit",
			// The bios + cloudinit flavor. Alpine also publishes a "tiny"
			// bootstrap that runs tiny-cloud instead of cloud-init: it has
			// no sudo key, no `cloud-init status`, and no plaintext-password
			// support, so stoat's seed and boot-progress polling would each
			// need a second implementation. The uefi flavor needs OVMF,
			// which stoat does not configure.
			//
			// The filename pins patch version 3.24.1, the same way the
			// Fedora entry pins its build, because dl-cdn prunes older
			// point releases.
			//
			// Ordered last among the alpine entries deliberately:
			// internal/tui/form.go's newForm() defaults a fresh form to the
			// first catalog entry with OS == "alpine", to keep the
			// pre-cloud-image default of the apkovl/live picker. Putting
			// this entry before alpine-standard would flip that default
			// silently.
			URL: "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/cloud/generic_alpine-3.24.1-x86_64-bios-cloudinit-r0.qcow2",
			// Alpine's per-image ".sha512" sidecar is a bare hex digest
			// with no filename column: a single 128-hex token, nothing
			// else on the line. parseChecksum requires the BSD "NAME
			// (file) = hex" form or at least two whitespace-separated
			// fields for the GNU form, so a lone hex string matches
			// neither and Resolve would fail. Left empty deliberately,
			// per Catalog()'s doc comment: Download() fetches without
			// verification instead.
			ChecksumURL: "",
			SSHUser:     "stoat",
			Notes:       "Alpine 3.24 cloud image, persistent, no manual install",
		},
	}
}

// Infer guesses a backend and OS for a bring-your-own image from its
// filename. It is a suggestion for the form to pre-fill, not a verdict: the
// user can override it, and an unrecognised name resolves to "ssh" with no
// OS guess.
//
// Hint matching decides the OS; the extension decides only the backend. An
// empty OS on an Alpine image sends guestShell("") to /bin/bash, a shell
// Alpine lacks, so the OS guess must fire on a hint match regardless of
// extension.
func Infer(filename string) (backend, os string) {
	lower := strings.ToLower(filename)

	var matchedOS string
	for _, o := range guest.All() {
		for _, hint := range o.FilenameHints {
			if strings.Contains(lower, hint) {
				matchedOS = o.Name
				break
			}
		}
		if matchedOS != "" {
			break
		}
	}

	switch {
	case matchedOS == "alpine" && strings.HasSuffix(lower, ".iso"):
		return "apkovl", "alpine"
	case strings.Contains(lower, "cloudimg"), strings.Contains(lower, "genericcloud"),
		strings.HasSuffix(lower, ".qcow2"), strings.HasSuffix(lower, ".img"):
		return "cloudinit", matchedOS
	default:
		return "ssh", matchedOS
	}
}

// client fetches small metadata (release indexes and checksum files), where
// a ceiling on the whole request is exactly right.
var client = &http.Client{Timeout: 30 * time.Second}

// downloadClient fetches images. http.Client.Timeout bounds the entire
// request, including the body read: the 30s that suits a checksum file
// would kill any image download over 30 seconds, which is nearly all of
// them ("context deadline exceeded ... while reading body"). This bounds
// only the phases that can hang without producing bytes, so a healthy
// transfer runs as long as it needs.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// stallTimeout aborts a download whose body goes this long without
// producing a byte. Without it, a half-dead connection hangs until the OS
// gives up on the socket, which can take many minutes with nothing on
// screen but 0 B/s.
const stallTimeout = 60 * time.Second

// downloadMirror is the base URL Download fetches ISO files from. It is a
// var (not the mirror const) purely so tests can point it at a local
// httptest.Server instead of the real Alpine mirror.
var downloadMirror = mirror

// Latest reads Alpine's published index so "latest" is never hardcoded, and
// returns the release matching flavor (e.g. "alpine-standard",
// "alpine-virt"): the index lists several flavors per release under the
// same file.
func Latest(flavor string) (*Release, error) {
	resp, err := client.Get(indexURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("index: %s", resp.Status)
	}
	var releases []Release
	if err := yaml.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		if releases[i].Flavor == flavor && strings.HasSuffix(releases[i].File, ".iso") {
			return &releases[i], nil
		}
	}
	return nil, fmt.Errorf("no %s iso in index", flavor)
}

// Resolve turns a catalog Entry into a concrete Release ready for Download.
//
// An entry backed by a "latest" index resolves via Latest() when Flavor is
// set. Gating on OS == "alpine" instead would fail: alpine-cloud is an
// alpine entry with a direct URL and no Flavor (see the Flavor field's doc
// comment), so it would go through Latest("") and find no release.
//
// Every other entry is a direct URL. The filename comes from the URL's
// path. When ChecksumURL is set, Resolve fetches that sums file and parses
// the matching SHA256 line. An entry with no ChecksumURL resolves with an
// empty SHA256, which Download() treats as "fetch without verification"
// rather than an error.
func Resolve(e Entry) (*Release, error) {
	if e.Flavor != "" {
		return Latest(e.Flavor)
	}

	u, err := url.Parse(e.URL)
	if err != nil {
		return nil, fmt.Errorf("entry %s: invalid URL: %w", e.ID, err)
	}
	file := path.Base(u.Path)

	r := &Release{File: file, URL: e.URL, Version: e.ID}

	if e.ChecksumURL != "" {
		sum, err := fetchChecksum(e.ChecksumURL, file)
		if err != nil {
			return nil, fmt.Errorf("entry %s: %w", e.ID, err)
		}
		r.SHA256 = sum
	}
	return r, nil
}

// fetchChecksum fetches a published sums file and returns the hex digest
// for filename. It handles two formats seen across the catalog's mirrors:
// GNU coreutils ("<hex>  <filename>" or "<hex> *<filename>", used by
// SHA256SUMS/SHA512SUMS/.SHA256) and BSD-style ("SHA256 (filename) =
// <hex>", used by Fedora's CHECKSUM, wrapped in a clearsigned PGP block
// whose armor lines match neither format and are skipped). The digest's
// hex length, 64 or 128, tells Download which algorithm to verify with;
// fetchChecksum does not need to know it.
func fetchChecksum(checksumURL, filename string) (string, error) {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return parseChecksum(body, filename)
}

// bsdChecksumLine matches a BSD-style digest line, e.g.
// "SHA256 (Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2) = 8465...".
var bsdChecksumLine = regexp.MustCompile(`^[A-Za-z0-9]+\s+\(([^)]+)\)\s*=\s*([0-9a-fA-F]+)\s*$`)

// parseChecksum scans a sums file (GNU coreutils or BSD format, see
// fetchChecksum) for the line matching filename and returns its lowercase
// hex digest.
func parseChecksum(body []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := bsdChecksumLine.FindStringSubmatch(line); m != nil {
			if m[1] == filename {
				return strings.ToLower(m[2]), nil
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		name = strings.TrimPrefix(name, "./")
		if name == filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", filename)
}

// newDigest picks the hash algorithm by the expected digest's hex length:
// 64 hex chars means sha256, 128 means sha512. Anything else, including
// empty (no checksum available), defaults to sha256, but callers only
// compare against a non-empty digest, so that default is never checked.
func newDigest(expected string) hash.Hash {
	if len(expected) == hex.EncodedLen(sha512.Size) {
		return sha512.New()
	}
	return sha256.New()
}

func fileDigest(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Download fetches r into isos/ and, when r.SHA256 is known, verifies it
// (sha256 or sha512, picked by digest length; see newDigest). It returns
// the path relative to the data root. An existing file with a matching
// digest is reused. Download renames the partial file only after the
// digest matches, or, for an entry with no published checksum,
// unconditionally on a full read, so an interrupted fetch never leaves a
// plausible-looking image behind.
//
// r.Verified is set true only when Download confirms the bytes against a
// digest, on a fresh download or a reused file. A configured checksum alone
// does not set it. It stays false whenever r.SHA256 is empty, so a caller
// can tell an unverified download from a verified one.
//
// r.URL, when set, is fetched directly (a catalog Entry's direct-URL
// image). Otherwise r.File is resolved against downloadMirror, the
// original Alpine-only behavior.
func Download(ctx context.Context, r *Release, progress func(done, total int64)) (string, error) {
	dir := filepath.Join(config.Root(), "isos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, r.File)
	rel := filepath.Join("isos", r.File)

	if r.SHA256 != "" {
		if sum, err := fileDigest(final, newDigest(r.SHA256)); err == nil && sum == r.SHA256 {
			r.Verified = true
			return rel, nil
		}
	}

	src := r.URL
	if src == "" {
		src = downloadMirror + r.File
	}

	// Derived from the caller's ctx, not context.Background(): cancelling
	// the request unblocks a Read that would otherwise never return.
	// Before this was reachable from outside, abandoning a download (the
	// TUI's esc) left the goroutine reading the socket until the stall
	// timer fired minutes later. The stall timer below cancels this same
	// ctx too.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}

	// Cancelling the request unblocks a Read that would otherwise never
	// return; each chunk that arrives pushes the deadline back out.
	// stalled distinguishes "no bytes for a minute" from any other
	// cancellation, so the user gets a reason instead of a bare
	// "context canceled".
	var stalled atomic.Bool
	stall := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	defer stall.Stop()

	// A unique .part per download, not the shared final+".part".
	//
	// os.Create truncates in place and grants no exclusivity. Two downloads
	// of the same image (reachable by cancelling and retrying, since a
	// cancelled download's goroutine outlives the call) opened the same
	// inode and wrote at independent offsets, interleaving into a corrupt
	// file.
	//
	// The checksum did not catch this: h reads only the bytes this
	// goroutine pulled off the network, never re-read from disk. Whichever
	// writer finished computed a correct digest of its own complete
	// stream, set Verified = true, and renamed an interleaved file into
	// place as a verified image. Entries with no ChecksumURL had no
	// backstop at all.
	//
	// CreateTemp gives each download its own inode, so concurrent
	// downloads can no longer touch each other's bytes; the last to
	// finish renames its own complete, digest-matched file over the final
	// name. The ".part" suffix stays because LocalImages (internal/core)
	// skips *.part files.
	f, err := os.CreateTemp(dir, r.File+".*.part")
	if err != nil {
		return "", err
	}
	part := f.Name()
	h := newDigest(r.SHA256)
	var done int64
	buf := make([]byte, 256*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(stallTimeout)
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(part)
				return "", werr
			}
			h.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(part)
			if stalled.Load() {
				return "", fmt.Errorf("download stalled: no data for %s", stallTimeout)
			}
			return "", rerr
		}
	}
	// Checked, not deferred: a Close error means bytes never reached disk.
	// The digest is computed from the read stream in memory, so a short
	// file would still pass verification and get renamed into place as
	// "verified".
	if err := f.Close(); err != nil {
		os.Remove(part)
		return "", err
	}

	if r.SHA256 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != r.SHA256 {
			os.Remove(part)
			return "", fmt.Errorf("checksum mismatch: got %s, want %s", got, r.SHA256)
		}
		r.Verified = true
	}
	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return "", err
	}
	return rel, nil
}
