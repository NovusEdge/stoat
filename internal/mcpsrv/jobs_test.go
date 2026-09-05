package mcpsrv

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestJobIDShape(t *testing.T) {
	re := regexp.MustCompile(`^j-[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for range 100 {
		id := newJobID()
		if !re.MatchString(id) {
			t.Fatalf("job id %q does not match %s", id, re)
		}
		if seen[id] {
			t.Fatalf("job id %q repeated", id)
		}
		seen[id] = true
	}
}

func TestSaveAndLoadJobs(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	j := job{
		ID: "j-9f3c1e2a", Argv: []string{"sleep", "60"}, User: "stoat",
		CWD: "/home/stoat", Dir: "/run/stoat/jobs/j-9f3c1e2a",
		Started: time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC),
	}
	if err := saveJob("dev", j); err != nil {
		t.Fatal(err)
	}
	// A second job must not replace the first: list_jobs works from the
	// host without ssh, and that is only true when the file accumulates.
	if err := saveJob("dev", job{ID: "j-00000001", Argv: []string{"true"}, User: "stoat"}); err != nil {
		t.Fatal(err)
	}
	got, err := loadJobs("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d jobs, want 2", len(got))
	}
	if got["j-9f3c1e2a"].Argv[1] != "60" {
		t.Fatalf("argv round trip lost data: %+v", got["j-9f3c1e2a"])
	}
	raw, err := os.ReadFile(filepath.Join(root, "dev", "jobs.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^# written by stoat; do not edit$`).Match(raw) {
		t.Fatalf("jobs.toml has no ownership comment:\n%s", raw)
	}
}

func TestLoadJobsOnAVMWithNoFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	if err := os.MkdirAll(filepath.Join(root, "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := loadJobs("dev")
	if err != nil {
		t.Fatalf("a VM that has never run a job is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d jobs", len(got))
	}
}
