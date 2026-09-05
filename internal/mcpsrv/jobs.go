package mcpsrv

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/tomlx"
)

var jobIDRE = regexp.MustCompile(`^j-[0-9a-f]{8}$`)

// job is one background command. The host owns the id and the record, so
// list_jobs answers without ssh and works on a stopped VM.
type job struct {
	ID      string    `toml:"-"`
	Argv    []string  `toml:"argv"`
	User    string    `toml:"user"`
	CWD     string    `toml:"cwd,omitempty"`
	Dir     string    `toml:"dir"`
	Started time.Time `toml:"started"`
}

type jobsFile struct {
	Schema int            `toml:"schema"`
	Jobs   map[string]job `toml:"jobs"`
}

func newJobID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "j-" + hex.EncodeToString(b[:])
}

func checkJobID(id string) (string, error) {
	if !jobIDRE.MatchString(id) {
		return "", fmt.Errorf("invalid job id %q: must match %s", id, jobIDRE)
	}
	return id, nil
}

func jobsPath(vm string) (string, error) {
	name, err := checkVMName(vm)
	if err != nil {
		return "", err
	}
	return filepath.Join(config.Root(), name, "jobs.toml"), nil
}

// loadJobs reads the registry. A VM that has never run a background job has
// no file, which is not an error.
func loadJobs(vm string) (map[string]job, error) {
	path, err := jobsPath(vm)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]job{}, nil
	}
	var f jobsFile
	if err := tomlx.Decode(path, &f, tomlx.Reject); err != nil {
		return nil, err
	}
	out := make(map[string]job, len(f.Jobs))
	for id, j := range f.Jobs {
		j.ID = id
		out[id] = j
	}
	return out, nil
}

// saveJob adds one job and rewrites the file. The write is to a temp file in
// the same directory and a rename, so a crash mid-write leaves the previous
// registry rather than a truncated one.
func saveJob(vm string, j job) error {
	jobs, err := loadJobs(vm)
	if err != nil {
		return err
	}
	jobs[j.ID] = j
	path, err := jobsPath(vm)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jobs-*.toml")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := fmt.Fprint(tmp, "# written by stoat; do not edit\n"); err != nil {
		return err
	}
	if err := toml.NewEncoder(tmp).Encode(jobsFile{Schema: 1, Jobs: jobs}); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
