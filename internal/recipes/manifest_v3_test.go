package recipes

import (
	"strings"
	"testing"
	"time"
)

const v3Manifest = `schema = 3
name = "docker"
script = "install.sh"

[params.user]
type    = "string"
default = "dev"
help    = "account added to the docker group"

[params.port]
type    = "int"
default = 2375

[params.tls]
type    = "bool"
default = true

[params.channel]
type    = "enum"
values  = ["stable", "test"]
default = "stable"

[params.authkey]
type     = "secret"
required = true

[outputs]
socket = "path of the docker socket"

[health]
check   = "docker info"
timeout = "45s"
`

func TestParseManifestV3(t *testing.T) {
	m, err := ParseManifest(writeManifestFile(t, t.TempDir(), v3Manifest))
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != 3 {
		t.Errorf("Schema = %d, want 3", m.Schema)
	}
	want := map[string]string{"user": "dev", "port": "2375", "tls": "true", "channel": "stable", "authkey": ""}
	for name, def := range want {
		p, ok := m.Params[name]
		if !ok {
			t.Fatalf("no param %q", name)
		}
		if p.Default != def {
			t.Errorf("%s default = %q, want %q", name, p.Default, def)
		}
		if p.Name != name {
			t.Errorf("%s Name = %q, want the map key", name, p.Name)
		}
	}
	if !m.Params["authkey"].Required {
		t.Error("authkey is not required")
	}
	if m.Outputs["socket"] == "" {
		t.Error("outputs.socket has no help text")
	}
	if m.Health.Check != "docker info" || m.Health.Duration() != 45*time.Second {
		t.Errorf("health = %+v", m.Health)
	}
	if got := m.SecretNames(); len(got) != 1 || got[0] != "authkey" {
		t.Errorf("SecretNames = %v, want [authkey]", got)
	}
}

// A recipe without [health] declares no check, and 30s is what a recipe that
// declares one but no timeout gets.
func TestHealthTimeoutDefaults(t *testing.T) {
	if (Health{}).Duration() != 0 {
		t.Error("an undeclared health check has no timeout")
	}
	if (Health{Check: "true"}).Duration() != 30*time.Second {
		t.Error("a declared check defaults to 30s")
	}
}

func TestParseManifestV3Errors(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{
			name: "no default and not required",
			body: "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[params.a]\ntype = \"string\"\n",
			want: `x.a: needs a default or required = true`,
		},
		{
			name: "bad type",
			body: "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[params.a]\ntype = \"float\"\ndefault = \"1\"\n",
			want: `x.a: type "float" is not one of string, int, bool, enum, secret`,
		},
		{
			name: "bad param name",
			body: "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[params.Auth-Key]\ntype = \"string\"\ndefault = \"a\"\n",
			want: `x: param "Auth-Key" must match [a-z][a-z0-9_]*`,
		},
		{
			name: "enum default not in values",
			body: "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[params.a]\ntype = \"enum\"\nvalues = [\"p\", \"q\"]\ndefault = \"r\"\n",
			want: `x.a: "r" is not one of p, q`,
		},
		{
			name: "secret with a default",
			body: "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[params.a]\ntype = \"secret\"\ndefault = \"hunter2\"\n",
			want: `x.a: a secret has no default`,
		},
		{
			name: "unsupported schema",
			body: "schema = 4\nname = \"x\"\nscript = \"i.sh\"\n",
			want: `schema 4 is newer than this stoat (3)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseManifest(writeManifestFile(t, t.TempDir(), tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestParseManifestRejectsInvalidHealthTimeout(t *testing.T) {
	for _, timeout := range []string{"soon", "0s", "-1s"} {
		t.Run(timeout, func(t *testing.T) {
			body := "schema = 3\nname = \"x\"\nscript = \"i.sh\"\n[health]\ncheck = \"true\"\ntimeout = \"" + timeout + "\"\n"
			_, err := ParseManifest(writeManifestFile(t, t.TempDir(), body))
			if err == nil || !strings.Contains(err.Error(), "health.timeout") {
				t.Fatalf("err = %v, want a health.timeout validation error", err)
			}
		})
	}
}

// Schema 2 manifests keep loading and carry no params, outputs or health.
func TestParseManifestSchema2StillLoads(t *testing.T) {
	body := "name = \"xfce\"\nscript = \"install.sh\"\nos = [\"alpine\"]\n"
	m, err := ParseManifest(writeManifestFile(t, t.TempDir(), body))
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != 2 {
		t.Errorf("Schema = %d, want 2 for a manifest with no schema key", m.Schema)
	}
	if len(m.Params) != 0 || len(m.Outputs) != 0 || m.Health.Check != "" {
		t.Errorf("schema 2 manifest carries v3 data: %+v", m)
	}
}
