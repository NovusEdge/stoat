package wire

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/novusedge/stoat/internal/core"
)

func TestLeadIndependentLeakCheck(t *testing.T) {
	v := core.VM{
		Name: "w", ConsolePassword: "hunter2",
		Base: "/home/u/.stoat/isos/base.qcow2",
		ISO:  "isos/alpine.iso",
		Paths: core.Paths{
			Dir: "/home/u/.stoat/w", Disk: "/home/u/.stoat/w/disk.qcow2",
			ConsoleLog: "/home/u/.stoat/w/console.log",
		},
	}
	b, err := json.Marshal(FromVM(v))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{"hunter2", "password", "/home/u", ".stoat/w"} {
		if strings.Contains(s, bad) {
			t.Errorf("marshalled VM leaks %q:\n%s", bad, s)
		}
	}
	if strings.Contains(s, `"recipes":null`) || strings.Contains(s, `"forwards":null`) {
		t.Errorf("nil slice marshalled as null:\n%s", s)
	}
	t.Logf("marshalled: %s", s)
}
