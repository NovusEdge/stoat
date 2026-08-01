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

	// SHA256 is the expected digest, despite the name not always a SHA-256
	// one: it holds whatever fetchChecksum found (Alpine's index is always
	// sha256; a catalog Entry's ChecksumURL may be sha512, e.g. Debian's
	// SHA512SUMS). Download picks the verification algorithm by this
	// string's hex length (64 -> sha256, 128 -> sha512) rather than by
	// name. Left empty, it means no checksum was available at all.
	SHA256 string `yaml:"sha256"`

	// URL, when set, is the full URL Download fetches from. It is left
	// empty by Latest() (Alpine), which instead resolves against
	// downloadMirror+File as before; Resolve() sets it for direct-URL
	// catalog entries (cloud images), whose files don't live under the
	// Alpine mirror.
	URL string

	// Verified reports whether Download actually checked the downloaded
	// bytes against a published digest. It starts false and Download sets
	// it true only after a byte-for-byte digest match (including the
	// existing-file reuse shortcut) — never on the strength of a
	// ChecksumURL merely being configured. When SHA256 is empty (no
	// checksum was available at Resolve time), Download fetches the file
	// anyway but Verified stays false, so callers can tell an unverified
	// download apart from a verified one instead of the two looking
	// identical.
	Verified bool
}

// Entry describes one image in stoat's catalog: what it is, where to fetch
// it, and which provisioning backend applies once it boots.
type Entry struct {
	ID      string
	OS      string
	Backend string // "apkovl" | "cloudinit" | "ssh"
	URL     string
	// Flavor selects which line of Alpine's latest-releases.yaml this
	// entry resolves to (e.g. "alpine-standard", "alpine-virt"). It is
	// only meaningful when OS == "alpine" — every other entry is a direct
	// URL and Resolve never looks at it.
	Flavor string
	// Variant is what distinguishes this entry from the others sharing its
	// OS — a version ("24.04 LTS"), a release ("13 (trixie)") or a build
	// ("standard" vs "virt"). It exists because the catalog stopped having
	// exactly one entry per OS: alpine has two that differ only in build,
	// and a picker grouping by OS has to label the second level with
	// something.
	//
	// It is a field rather than something derived from ID because deriving
	// it works for four entries and produces nonsense for two: stripping
	// the "<os>-" prefix labels both fedora-cloud and arch-cloud "cloud".
	Variant string
	// Size is the download's approximate size, for display only. It is
	// DECLARED rather than fetched because Catalog() promises that listing or
	// picking needs no network access, and a HEAD per entry when the form
	// opens would cost four round trips and break the picker offline.
	//
	// It is therefore approximate and drifts as images are rebuilt in place.
	// Nothing may allocate, verify or preallocate against it — the real size
	// comes from Content-Length at download time, and the real check is the
	// checksum. It exists so the picker can say why alpine-virt is worth
	// choosing over alpine-standard, which is 5x its size.
	//
	// Measured 2026-08-01 by Content-Length, rounded to whole MiB.
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

// ByOS groups the catalog for a two-level picker: choose an OS, then a
// variant within it. Both levels keep the catalog's own hand-written order
// rather than sorting — the catalog is ordered deliberately (the most
// generally useful image first), and sorting would replace that judgement
// with alphabetical noise.
//
// The flat Catalog() remains the API everything else uses; this is a view of
// it for display, so nothing downstream has to care that the shape changed.
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
// ChecksumURL points at a *published sums file* to be parsed at download
// time (never a frozen hash — cloud images roll forward in place). Where a
// distro has no stable, algorithm-matching sums URL to point at, ChecksumURL
// is left empty and that is called out below; Download() then fetches
// without verification rather than silently failing on a guessed URL.
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
			// Debian publishes SHA512SUMS (not SHA256) alongside this
			// image. Verified 2026-07-31 by GET: 128-hex digests, "<hex>
			// <filename>" (GNU coreutils) format. parseChecksum/Download
			// now pick the hash algorithm by digest length (64 hex ->
			// sha256, 128 hex -> sha512), so this verifies correctly.
			ChecksumURL: "https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS",
			SSHUser:     "stoat",
			Notes:       "Debian 13 (trixie) generic cloud image",
		},
		{
			ID:      "fedora-cloud",
			OS:      "fedora",
			Variant: "44",
			Size:    557 * mib,
			// Fedora keeps roughly N/N-1/N-2 under releases/ with no
			// latest/ symlink for cloud images, so this entry needs a
			// manual bump every Fedora cycle and WILL break (releases/
			// -> archives.fedoraproject.org, filename changes too) once
			// the pinned release ages out. Verified 2026-07-31 by
			// GET: Fedora 44 is current and lives under releases/
			// (200, ~583MB, Last-Modified 2026-04-22).
			Backend: "cloudinit",
			URL:     "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2",
			// Fedora's per-release CHECKSUM is BSD format ("SHA256
			// (filename) = hex") inside a clearsigned PGP block, not the
			// GNU "<hex>  <filename>" form. parseChecksum now has a BSD
			// branch (small: one regexp, matched before falling back to
			// the GNU split) so this verifies too; the PGP armor lines
			// around it are simply lines that don't match either format
			// and are skipped. The compose suffix (here "1.7") is baked
			// into both this URL and ChecksumURL and changes per respin —
			// re-verify both together on the next bump.
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
			// Verified 2026-07-30 by GET: this mirror does publish a
			// per-image ".SHA256" sidecar (200, "<64hex>  <filename>"),
			// same format and algorithm parseChecksum already handles.
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
			// Alpine's checksum isn't a separate sums file: each release
			// in latest-releases.yaml embeds its own sha256 inline, and
			// Resolve() reads it via Latest(). ChecksumURL is left empty
			// deliberately — it is not "unverified", verification just
			// happens through the index instead.
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
			// above) — virt is built for VM guests specifically: virtio
			// drivers only, no baremetal hardware support, meaningfully
			// smaller. That's a better default for a tool whose only
			// target is QEMU, so it's offered alongside standard rather
			// than replacing it.
			ChecksumURL: "",
			SSHUser:     "root",
			Notes:       "Alpine virt live ISO (smaller, virtio-only, built for VM guests)",
		},
	}
}

// Infer guesses a backend and OS for a bring-your-own image from its
// filename alone. It is a suggestion for the form to pre-fill, never a
// verdict: the user can always override it, and an unrecognised name
// resolves to "ssh" with no OS guess rather than a wrong guess.
func Infer(filename string) (backend, os string) {
	lower := strings.ToLower(filename)
	switch {
	case strings.Contains(lower, "alpine") && strings.HasSuffix(lower, ".iso"):
		return "apkovl", "alpine"
	case strings.Contains(lower, "cloudimg"), strings.Contains(lower, "genericcloud"),
		strings.HasSuffix(lower, ".qcow2"), strings.HasSuffix(lower, ".img"):
		return "cloudinit", ""
	default:
		return "ssh", ""
	}
}

// client fetches small metadata — release indexes and checksum files — where
// a ceiling on the whole request is exactly right.
var client = &http.Client{Timeout: 30 * time.Second}

// downloadClient fetches images. http.Client.Timeout bounds the ENTIRE
// request including reading the body, so the 30s that suits a checksum file
// kills any image download that takes longer than 30 seconds — which is all
// of them ("context deadline exceeded ... while reading body"). Bound only
// the phases that can hang without producing bytes, and let a healthy
// transfer run as long as it needs to.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

// stallTimeout aborts a download whose body has gone this long without
// producing a single byte. Removing the overall timeout above would otherwise
// mean a half-dead connection hangs until the OS gives up on the socket,
// which can be many minutes with nothing on screen but 0 B/s.
const stallTimeout = 60 * time.Second

// downloadMirror is the base URL Download fetches ISO files from. It is a
// var (not the mirror const) purely so tests can point it at a local
// httptest.Server instead of the real Alpine mirror.
var downloadMirror = mirror

// Latest reads Alpine's published index so "latest" is never hardcoded, and
// returns the release matching flavor (e.g. "alpine-standard",
// "alpine-virt") — the index lists several flavors per release under the
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
// Alpine (the one entry backed by a "latest" index rather than a direct
// file) is resolved via Latest(), exactly as before this generalization.
// Every other entry is a direct URL: the filename is taken from the URL's
// path, and when ChecksumURL is set, Resolve fetches that sums file and
// parses out the matching SHA256 line. An entry with no ChecksumURL
// resolves with an empty SHA256, which Download() treats as "fetch without
// verification" rather than an error.
func Resolve(e Entry) (*Release, error) {
	if e.OS == "alpine" {
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

// fetchChecksum fetches a published sums file and returns the hex digest for
// filename. It handles both formats seen across the catalog's real
// mirrors: GNU coreutils ("<hex>  <filename>" or "<hex> *<filename>", as
// used by SHA256SUMS/SHA512SUMS/.SHA256) and BSD-style ("SHA256 (filename)
// = <hex>", as used by Fedora's CHECKSUM, which also arrives wrapped in a
// clearsigned PGP block — the armor lines simply match neither format and
// are skipped). The digest's own hex length (64 vs 128) tells Download which
// algorithm to verify with; fetchChecksum does not need to know it.
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

// newDigest picks the hash algorithm to verify with, by the expected
// digest's hex length: 64 hex chars -> sha256, 128 -> sha512. Anything else
// (including empty, the "no checksum available" case) defaults to sha256,
// but callers only compare against a non-empty expected digest, so the
// default is never actually checked in that case.
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
// (sha256 or sha512, picked by digest length — see newDigest), returning the
// path relative to the data root. An existing file with a matching digest is
// reused. The partial download is renamed only after the digest matches (or,
// for an entry with no published checksum, unconditionally on a full read),
// so an interrupted fetch never leaves a plausible-looking image behind.
//
// r.Verified is set true only when Download itself confirmed the bytes
// against a digest (fresh download or reused file) — never merely because a
// checksum was configured. It stays false whenever r.SHA256 is empty, so a
// caller can tell an unverified download apart from a verified one instead
// of the two being indistinguishable.
//
// r.URL, when set, is fetched directly (a catalog Entry's direct-URL image);
// otherwise r.File is resolved against downloadMirror, preserving the
// original Alpine-only behavior.
func Download(r *Release, progress func(done, total int64)) (string, error) {
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

	ctx, cancel := context.WithCancel(context.Background())
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

	// Cancelling the request is what unblocks a Read that will never return;
	// every chunk that does arrive pushes the deadline back out.
	// stalled distinguishes "no bytes for a minute" from any other
	// cancellation, so the user gets a reason rather than a bare
	// "context canceled" they can't act on.
	var stalled atomic.Bool
	stall := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	defer stall.Stop()

	part := final + ".part"
	f, err := os.Create(part)
	if err != nil {
		return "", err
	}
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
	// Checked, not deferred: a Close error here would mean bytes never
	// reached disk, and the digest is computed from the read stream in
	// memory — so a short file would pass verification and get renamed into
	// place as a "verified" image.
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
