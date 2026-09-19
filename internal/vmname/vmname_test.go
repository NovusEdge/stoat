package vmname

import (
	"errors"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/coreerr"
)

func TestValidateAccepts(t *testing.T) {
	for _, name := range []string{
		"work", "Work", "w", "1", "work-1", "work_1", "work.1", "a.b-c_d",
		"console", "communicator", "lpt", "com0", "com10", "nullx",
	} {
		if err := Validate(name); err != nil {
			t.Errorf("Validate(%q) = %v; want nil", name, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		want string // a fragment of the message that names the problem
	}{
		{"", "required"},
		{"   ", "required"},
		{"work ", "whitespace"},
		{" work", "whitespace"},
		{".", "path traversal"},
		{"..", "path traversal"},
		{"work/evil", "path separator"},
		{`work\evil`, "path separator"},
		{"/etc/passwd", "path separator"},
		{"work\x00", "null byte"},
		{"-lead", "dash"},
		{"nul", "reserved device name"},
		{"NUL", "reserved device name"},
		{"Nul.txt", "reserved device name"},
		{"con", "reserved device name"},
		{"com1", "reserved device name"},
		{"LPT9.log", "reserved device name"},
		{"nul.", "reserved device name"},
		{".hidden", "must match"},
		{"wörk!", "must match"},
		{"work⁄evil", "must match"},
	}
	for _, c := range cases {
		err := Validate(c.name)
		if err == nil {
			t.Errorf("Validate(%q) = nil; want an error", c.name)
			continue
		}
		if !errors.Is(err, coreerr.ErrInvalidSpec) {
			t.Errorf("Validate(%q) = %v; want it to wrap ErrInvalidSpec", c.name, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Validate(%q) = %q; want it to mention %q", c.name, err, c.want)
		}
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("Validate(%q) = %q; want it to state what a valid name looks like", c.name, err)
		}
	}
}
