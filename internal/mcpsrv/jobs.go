package mcpsrv

import (
	"fmt"
	"time"
)

// job is one background command. The host owns the id and the record, so
// list_jobs answers without ssh and works on a stopped VM.
type job struct {
	ID      string
	Argv    []string
	User    string
	CWD     string
	Dir     string
	Started time.Time
}

// newJobID is not implemented yet. Task 12 returns "j-" plus 8 random hex
// characters.
func newJobID() string {
	return ""
}

// loadJobs is not implemented yet. Task 12 reads
// config.Root()/<vm>/jobs.toml through tomlx.Decode; a VM with no file
// returns an empty map, not an error.
func loadJobs(vm string) (map[string]job, error) {
	return nil, fmt.Errorf("loadJobs(%q): not implemented", vm)
}

// saveJob is not implemented yet. Task 12 adds j to the registry and
// rewrites jobs.toml through a temp file and rename.
func saveJob(vm string, j job) error {
	return fmt.Errorf("saveJob(%q, %q): not implemented", vm, j.ID)
}
