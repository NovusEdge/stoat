package installer

import (
	"reflect"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantID     string
		wantIDLike []string
	}{
		{
			name:    "arch has no ID_LIKE",
			content: "NAME=\"Arch Linux\"\nID=arch\nBUILD_ID=rolling\n",
			wantID:  "arch",
		},
		{
			name:       "ubuntu is debian-like, values quoted",
			content:    "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n",
			wantID:     "ubuntu",
			wantIDLike: []string{"debian"},
		},
		{
			name:       "ID_LIKE may hold several, space separated and quoted",
			content:    "ID=rhel\nID_LIKE=\"fedora centos\"\n",
			wantID:     "rhel",
			wantIDLike: []string{"fedora", "centos"},
		},
		{
			name:    "comments and blank lines are ignored",
			content: "# a comment\n\nID=debian\n",
			wantID:  "debian",
		},
		{
			name:    "single-quoted value",
			content: "ID='fedora'\n",
			wantID:  "fedora",
		},
		{
			name:       "CRLF line endings",
			content:    "ID=debian\r\nID_LIKE=\"debian\"\r\n",
			wantID:     "debian",
			wantIDLike: []string{"debian"},
		},
		{
			name:    "empty content yields nothing",
			content: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, idLike := ParseOSRelease(tt.content)
			if id != tt.wantID {
				t.Errorf("id = %q, want %q", id, tt.wantID)
			}
			if len(idLike) != 0 || len(tt.wantIDLike) != 0 {
				if !reflect.DeepEqual(idLike, tt.wantIDLike) {
					t.Errorf("idLike = %#v, want %#v", idLike, tt.wantIDLike)
				}
			}
		})
	}
}

func TestDistroFrom(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		idLike []string
		want   Distro
	}{
		{"arch by id", "arch", nil, DistroArch},
		{"debian by id", "debian", nil, DistroDebian},
		{"fedora by id", "fedora", nil, DistroFedora},
		{"ubuntu falls back to ID_LIKE", "ubuntu", []string{"debian"}, DistroDebian},
		{"rhel falls back to ID_LIKE", "rhel", []string{"fedora", "centos"}, DistroFedora},
		{"cachyos is arch-like", "cachyos", []string{"arch"}, DistroArch},
		{"id wins over id_like", "debian", []string{"arch"}, DistroDebian},
		{"alpine is unknown", "alpine", nil, DistroUnknown},
		{"empty is unknown", "", nil, DistroUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DistroFrom(tt.id, tt.idLike); got != tt.want {
				t.Errorf("DistroFrom(%q, %v) = %v, want %v", tt.id, tt.idLike, got, tt.want)
			}
		})
	}
}

func TestInstallCmd(t *testing.T) {
	qemu := Pkg{Arch: "qemu-full", Debian: "qemu-system-x86", Fedora: "qemu-kvm"}

	tests := []struct {
		name string
		d    Distro
		want []string
	}{
		{"arch", DistroArch, []string{"sudo pacman -S --needed qemu-full"}},
		{"debian", DistroDebian, []string{"sudo apt install qemu-system-x86"}},
		{"fedora", DistroFedora, []string{"sudo dnf install qemu-kvm"}},
		{"unknown names the packages, invents no command", DistroUnknown, []string{"install: qemu-full / qemu-system-x86 / qemu-kvm"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.InstallCmd(qemu); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("InstallCmd() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
