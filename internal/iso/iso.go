// Package iso finds and downloads Alpine images into the stoat data root.
package iso

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/novusedge/stoat/internal/config"
)

const (
	mirror   = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/x86_64/"
	indexURL = mirror + "latest-releases.yaml"
	flavor   = "alpine-standard"
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
	ID          string
	OS          string
	Arch        string
	Backend     string // "apkovl" | "cloudinit" | "ssh"
	URL         string
	ChecksumURL string
	SSHUser     string
	Notes       string
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
			Arch:        "amd64",
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
			ID:      "debian-12",
			OS:      "debian",
			Arch:    "amd64",
			Backend: "cloudinit",
			URL:     "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
			// Debian publishes SHA512SUMS (not SHA256) alongside this
			// image. Verified 2026-07-30 by GET: 128-hex digests, "<hex>
			// <filename>" (GNU coreutils) format. parseChecksum/Download
			// now pick the hash algorithm by digest length (64 hex ->
			// sha256, 128 hex -> sha512), so this verifies correctly.
			ChecksumURL: "https://cloud.debian.org/images/cloud/bookworm/latest/SHA512SUMS",
			SSHUser:     "stoat",
			Notes:       "Debian 12 (bookworm) generic cloud image",
		},
		{
			ID:   "fedora-cloud",
			OS:   "fedora",
			Arch: "amd64",
			// Verified 2026-07-30 by HEAD/GET: Fedora 40 aged out of
			// releases/ into archives.fedoraproject.org (filename also
			// changed). Fedora 43 is current and still lives under
			// releases/ (200, ~583MB, Last-Modified 2025-10-23) — that
			// path is far more stable than pinning an archived compose,
			// so this points at 43 rather than the archive.
			Backend: "cloudinit",
			URL:     "https://download.fedoraproject.org/pub/fedora/linux/releases/43/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2",
			// Fedora's per-release CHECKSUM is BSD format ("SHA256
			// (filename) = hex") inside a clearsigned PGP block, not the
			// GNU "<hex>  <filename>" form. parseChecksum now has a BSD
			// branch (small: one regexp, matched before falling back to
			// the GNU split) so this verifies too; the PGP armor lines
			// around it are simply lines that don't match either format
			// and are skipped.
			ChecksumURL: "https://download.fedoraproject.org/pub/fedora/linux/releases/43/Cloud/x86_64/images/Fedora-Cloud-43-1.6-x86_64-CHECKSUM",
			SSHUser:     "stoat",
			Notes:       "Fedora Cloud Base qcow2",
		},
		{
			ID:      "arch-cloud",
			OS:      "arch",
			Arch:    "amd64",
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
			Arch:    "amd64",
			Backend: "apkovl",
			URL:     indexURL,
			// Alpine's checksum isn't a separate sums file: each release
			// in latest-releases.yaml embeds its own sha256 inline, and
			// Resolve() reads it via Latest(). ChecksumURL is left empty
			// deliberately — it is not "unverified", verification just
			// happens through the index instead.
			ChecksumURL: "",
			SSHUser:     "root",
			Notes:       "Alpine standard live ISO (existing apkovl path)",
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

var client = &http.Client{Timeout: 30 * time.Second}

// downloadMirror is the base URL Download fetches ISO files from. It is a
// var (not the mirror const) purely so tests can point it at a local
// httptest.Server instead of the real Alpine mirror.
var downloadMirror = mirror

// Latest reads Alpine's published index so "latest" is never hardcoded.
func Latest() (*Release, error) {
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
		return Latest()
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

	resp, err := client.Get(src)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}

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
			return "", rerr
		}
	}
	f.Close()

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
