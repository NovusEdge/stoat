package recipes

import (
	"slices"
	"strings"
	"testing"
)

func TestParseMetadataMissingBlock(t *testing.T) {
	// No "# stoat:" tags at all: a recipe predating the contract, or one
	// that simply declares nothing. Zero Metadata, no error.
	m, err := ParseMetadata("#!/bin/sh\n# just a plain comment\nset -e\necho hi\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "" || m.Description != "" || m.OS != nil || m.Requires != nil || m.Stages != nil {
		t.Errorf("m = %+v, want zero value", m)
	}
}

func TestParseMetadataFullBlock(t *testing.T) {
	body := "#!/bin/sh\n" +
		"# stoat:name        xfce\n" +
		"# stoat:description XFCE desktop with a graphical login\n" +
		"# stoat:os          alpine, ubuntu, debian, arch\n" +
		"# stoat:requires    systemd\n" +
		"# stoat:stages      install, configure, enable\n" +
		"set -e\n"
	m, err := ParseMetadata(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "xfce" {
		t.Errorf("Name = %q, want xfce", m.Name)
	}
	if m.Description != "XFCE desktop with a graphical login" {
		t.Errorf("Description = %q", m.Description)
	}
	if want := []string{"alpine", "ubuntu", "debian", "arch"}; !slices.Equal(m.OS, want) {
		t.Errorf("OS = %v, want %v", m.OS, want)
	}
	if want := []string{"systemd"}; !slices.Equal(m.Requires, want) {
		t.Errorf("Requires = %v, want %v", m.Requires, want)
	}
	if want := []string{"install", "configure", "enable"}; !slices.Equal(m.Stages, want) {
		t.Errorf("Stages = %v, want %v", m.Stages, want)
	}
}

func TestParseMetadataUnknownKey(t *testing.T) {
	_, err := ParseMetadata("#!/bin/sh\n# stoat:flavor spicy\nset -e\n")
	if err == nil || !strings.Contains(err.Error(), `unknown key "flavor"`) {
		t.Errorf("err = %v, want an unknown-key error", err)
	}
}

func TestParseMetadataDuplicateKey(t *testing.T) {
	body := "#!/bin/sh\n# stoat:name a\n# stoat:name b\nset -e\n"
	_, err := ParseMetadata(body)
	if err == nil || !strings.Contains(err.Error(), `duplicate key "name"`) {
		t.Errorf("err = %v, want a duplicate-key error", err)
	}
}

func TestParseMetadataMalformedLine(t *testing.T) {
	// "# stoat:" with nothing after it at all, no key and not even an empty
	// one, is malformed. Distinct from a known key with an empty value.
	_, err := ParseMetadata("#!/bin/sh\n# stoat:\nset -e\n")
	if err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("err = %v, want a malformed-line error", err)
	}
}

func TestParseMetadataEmptyValue(t *testing.T) {
	_, err := ParseMetadata("#!/bin/sh\n# stoat:name\nset -e\n")
	if err == nil || !strings.Contains(err.Error(), `"name" has no value`) {
		t.Errorf("err = %v, want an empty-value error", err)
	}
}

func TestParseMetadataTagsAfterFrontMatterAreNotHonoured(t *testing.T) {
	// A tag appearing after the first non-comment, non-blank line is not
	// front matter. It is left as an ordinary comment, not parsed and not
	// reported as an error either.
	body := "#!/bin/sh\nset -e\n# stoat:name too-late\n"
	m, err := ParseMetadata(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "" {
		t.Errorf("Name = %q, want empty: the tag came after front matter", m.Name)
	}
}

func TestParseMetadataCollectsMultipleErrors(t *testing.T) {
	body := "#!/bin/sh\n# stoat:flavor spicy\n# stoat:name\nset -e\n"
	_, err := ParseMetadata(body)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `unknown key "flavor"`) || !strings.Contains(err.Error(), `"name" has no value`) {
		t.Errorf("err = %v, want both problems reported", err)
	}
}

func TestParseMetadataBlankLinesDoNotEndFrontMatter(t *testing.T) {
	body := "#!/bin/sh\n\n# stoat:name xfce\n\nset -e\n"
	m, err := ParseMetadata(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "xfce" {
		t.Errorf("Name = %q, want xfce", m.Name)
	}
}

func TestUnsupportedReasonOS(t *testing.T) {
	m := Metadata{OS: []string{"ubuntu", "debian"}}
	if r := UnsupportedReason("alpine", m); r == "" {
		t.Error("want a reason, alpine is not in the declared os list")
	}
	if r := UnsupportedReason("ubuntu", m); r != "" {
		t.Errorf("want no reason, got %q", r)
	}
}

func TestUnsupportedReasonRequiresSystemd(t *testing.T) {
	m := Metadata{Requires: []string{"systemd"}}
	// The exact wording from docs/design/guest-subsystem.md §5.
	if got, want := UnsupportedReason("alpine", m), "requires systemd, alpine uses openrc"; got != want {
		t.Errorf("UnsupportedReason(alpine) = %q, want %q", got, want)
	}
	if r := UnsupportedReason("ubuntu", m); r != "" {
		t.Errorf("want no reason for ubuntu, got %q", r)
	}
}

// TestUnsupportedReasonSystemdPerOS covers every registry OS against the
// systemd capability: only alpine (OpenRC) must be rejected.
func TestUnsupportedReasonSystemdPerOS(t *testing.T) {
	m := Metadata{Requires: []string{"systemd"}}
	for _, tc := range []struct {
		os      string
		rejects bool
	}{
		{"alpine", true},
		{"ubuntu", false},
		{"debian", false},
		{"fedora", false},
		{"arch", false},
	} {
		r := UnsupportedReason(tc.os, m)
		if tc.rejects && r == "" {
			t.Errorf("%s: want a reason, has no systemd", tc.os)
		}
		if !tc.rejects && r != "" {
			t.Errorf("%s: want no reason, got %q", tc.os, r)
		}
	}
}

func TestUnsupportedReasonUnknownOS(t *testing.T) {
	// A BYO image whose OS string isn't in the registry: a "requires"
	// capability can't be verified, so it's rejected rather than assumed.
	if r := UnsupportedReason("plan9", Metadata{Requires: []string{"systemd"}}); r == "" {
		t.Error("want a reason, plan9 is not a recognised OS")
	}
	// No requires at all: nothing to verify, so an unknown OS is not blocked.
	if r := UnsupportedReason("plan9", Metadata{}); r != "" {
		t.Errorf("want no reason with no requires, got %q", r)
	}
}

func TestUnsupportedReasonUnknownCapability(t *testing.T) {
	m := Metadata{Requires: []string{"warp-drive"}}
	if r := UnsupportedReason("ubuntu", m); !strings.Contains(r, `unknown capability "warp-drive"`) {
		t.Errorf("r = %q, want an unknown-capability reason", r)
	}
}

func TestUnsupportedReasonNone(t *testing.T) {
	if r := UnsupportedReason("ubuntu", Metadata{}); r != "" {
		t.Errorf("want no reason for empty metadata, got %q", r)
	}
}

// v2 recipes use recipe.toml for metadata, not in-file front-matter.
// See manifest_test.go for v2 capability/OS filtering tests.
