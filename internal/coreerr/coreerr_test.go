package coreerr

import (
	"errors"
	"io/fs"
	"testing"
)

func TestSuggest(t *testing.T) {
	known := []string{"alpine-standard", "alpine-virt", "debian-13"}
	tests := []struct{ name, want string }{
		{"alpine-standrd", "alpine-standard"},
		{"debian13", "debian-13"},
		{"alpine", "alpine-standard"},
		{"windows-11", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := Suggest(tt.name, known); got != tt.want {
			t.Errorf("Suggest(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNoVMErrorText(t *testing.T) {
	err := &NoVMError{Name: "qol", Known: func() []string { return []string{"qol1", "web"} }}
	if got, want := err.Error(), `no VM "qol" (did you mean "qol1"?); see stoat ls`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if got, want := err.MCPText(), `no VM "qol" (did you mean "qol1"?); see list_vms`; got != want {
		t.Errorf("MCPText() = %q, want %q", got, want)
	}
	if !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("NoVMError must match ErrNotFound and fs.ErrNotExist")
	}
	bare := &NoVMError{Name: "nope"}
	if got, want := bare.Error(), `no VM "nope"; see stoat ls`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestNoImageErrorText(t *testing.T) {
	err := &NoImageError{Spec: "alpine-viirt", Known: []string{"alpine-standard", "alpine-virt"}}
	want := `no image "alpine-viirt" (did you mean "alpine-virt"?); run stoat images`
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("NoImageError must match ErrNotFound")
	}
}
