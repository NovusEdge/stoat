package qemu

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/novusedge/stoat/internal/config"
)

// fakeQMP serves one QMP session on v's socket and records each command. It
// answers query-block with the given JSON and every other command with the
// given reply.
func fakeQMP(t *testing.T, v *config.VM, queryBlock string, reply string) *[]map[string]any {
	t.Helper()
	l, err := net.Listen("unix", v.QMPPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var mu sync.Mutex
	var got []map[string]any
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = c.Write([]byte(`{"QMP":{"version":{},"capabilities":[]}}` + "\n"))
		dec := json.NewDecoder(c)
		for {
			var req map[string]any
			if dec.Decode(&req) != nil {
				return
			}
			out := `{"return":{}}`
			switch req["execute"] {
			case "qmp_capabilities":
			case "query-block":
				out = `{"return":` + queryBlock + `}`
			default:
				mu.Lock()
				got = append(got, req)
				mu.Unlock()
				out = reply
			}
			_, _ = c.Write([]byte(out + "\n"))
		}
	}()
	return &got
}

func qmpVM(t *testing.T) *config.VM {
	t.Helper()
	// A short dir: unix socket paths cap near 108 bytes.
	dir, err := os.MkdirTemp("", "qmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return &config.VM{Name: "work", Dir: dir}
}

func blocks(v *config.VM) string {
	return `[
	 {"device":"ide1-cd0","inserted":{"file":"/iso/x.iso","image":{}}},
	 {"device":"virtio0","inserted":{"file":"` + v.DiskPath() + `","image":{"snapshots":[
	   {"id":"1","name":"clean","vm-state-size":0,"date-sec":1785844800,"date-nsec":7}]}}}]`
}

// The snapshot must target the qcow2's own device, not the cdrom listed first.
func TestSnapshotSaveTargetsTheDisk(t *testing.T) {
	v := qmpVM(t)
	got := fakeQMP(t, v, blocks(v), `{"return":{}}`)
	if err := SnapshotSave(v, "clean"); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{
		"execute":   "blockdev-snapshot-internal-sync",
		"arguments": map[string]any{"device": "virtio0", "name": "clean"},
	}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("commands = %v, want %v", *got, want)
	}
}

func TestSnapshotDeleteTargetsTheDisk(t *testing.T) {
	v := qmpVM(t)
	got := fakeQMP(t, v, blocks(v), `{"return":{"name":"clean"}}`)
	if err := SnapshotDelete(v, "clean"); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || (*got)[0]["execute"] != "blockdev-snapshot-delete-internal-sync" {
		t.Errorf("commands = %v", *got)
	}
}

func TestSnapshotListReadsQueryBlock(t *testing.T) {
	v := qmpVM(t)
	fakeQMP(t, v, blocks(v), `{"return":{}}`)
	got, err := SnapshotList(v)
	if err != nil {
		t.Fatal(err)
	}
	want := []SnapshotEntry{{Name: "clean", DateSec: 1785844800, DateNsec: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SnapshotList = %+v, want %+v", got, want)
	}
}

// QEMU's own refusal (a duplicate tag, a format without snapshots) comes back
// as ErrMonitorRejected with QEMU's description.
func TestSnapshotSaveSurfacesQEMUError(t *testing.T) {
	v := qmpVM(t)
	fakeQMP(t, v, blocks(v), `{"error":{"class":"GenericError","desc":"snapshot 'clean' exists"}}`)
	err := SnapshotSave(v, "clean")
	if !errors.Is(err, ErrMonitorRejected) {
		t.Fatalf("err = %v, want ErrMonitorRejected", err)
	}
}

func TestSnapshotFailsWhenNoDeviceBacksTheDisk(t *testing.T) {
	v := qmpVM(t)
	fakeQMP(t, v, `[{"device":"virtio0","inserted":{"file":"`+filepath.Join(v.Dir, "other.qcow2")+`","image":{}}}]`, `{"return":{}}`)
	if err := SnapshotSave(v, "clean"); !errors.Is(err, ErrMonitorRejected) {
		t.Fatalf("err = %v, want ErrMonitorRejected", err)
	}
}
