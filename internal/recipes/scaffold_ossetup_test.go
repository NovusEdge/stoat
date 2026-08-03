package recipes

import "testing"

// TestOSSetup: a known OS returns the registry's package-manager preamble and
// install verb; an unknown OS falls back to no preamble and a generic
// install comment.
func TestOSSetup(t *testing.T) {
	setup, install := osSetup("alpine")
	if install != "apk add " {
		t.Errorf("osSetup(alpine) install = %q, want %q", install, "apk add ")
	}
	if setup == "" {
		t.Error("osSetup(alpine) setup is empty, want the community-repo preamble")
	}

	setup, install = osSetup("nonexistent-os")
	if setup != "" || install != "# install: " {
		t.Errorf("osSetup(unknown) = %q, %q, want \"\", \"# install: \"", setup, install)
	}
}
