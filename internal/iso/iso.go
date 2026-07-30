// Package iso finds and downloads Alpine images into the stoat data root.
package iso

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

// Release is one entry from Alpine's latest-releases.yaml.
type Release struct {
	Flavor  string `yaml:"flavor"`
	File    string `yaml:"file"`
	Version string `yaml:"version"`
	SHA256  string `yaml:"sha256"`
}

var client = &http.Client{Timeout: 30 * time.Second}

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

// List returns the ISO filenames already present in the data root.
func List() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(config.Root(), "isos"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".iso") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Download fetches r into isos/ and verifies its published checksum, returning
// the path relative to the data root. An existing file with a matching sum is
// reused. The partial download is renamed only after the hash matches, so an
// interrupted fetch never leaves a plausible-looking ISO behind.
func Download(r *Release, progress func(done, total int64)) (string, error) {
	dir := filepath.Join(config.Root(), "isos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	final := filepath.Join(dir, r.File)
	rel := filepath.Join("isos", r.File)

	if sum, err := sha256File(final); err == nil && sum == r.SHA256 {
		return rel, nil
	}

	resp, err := client.Get(mirror + r.File)
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
	h := sha256.New()
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

	if got := hex.EncodeToString(h.Sum(nil)); got != r.SHA256 {
		os.Remove(part)
		return "", fmt.Errorf("checksum mismatch: got %s, want %s", got, r.SHA256)
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return rel, nil
}
