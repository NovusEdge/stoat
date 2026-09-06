//go:build linux

package qemu

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novusedge/stoat/internal/config"
)

func installCPUProbe(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	probe := filepath.Join(dir, Binary)
	script := `#!/bin/sh
printf '%s\n' "$*" > "$CPU_PROBE_ARGS"
case "$CPU_PROBE_MODE" in
timeout)
  sleep 10
  ;;
stderr)
  echo "qemu/kvm probe stderr sentinel" >&2
  exit 17
  ;;
*)
  echo '{"QMP":{"version":{"qemu":{"major":11,"minor":1,"micro":1}}}}'
  while IFS= read -r line; do
    case "$line" in
    *qmp_capabilities*) echo '{"return":{}}' ;;
    *query-cpu-model-expansion*)
      case "$CPU_PROBE_MODE" in
      error) echo '{"error":{"class":"GenericError","desc":"probe rejected"}}' ;;
      malformed) echo 'not json' ;;
      missing) echo '{"return":{"model":{"name":"host"}}}' ;;
      *)
        echo '{"event":"RESET"}'
        echo '{"return":{"model":{"name":"host","props":{"family":6,"vendor":"GenuineIntel","model":140,"model-id":"Intel(R) Core(TM) i7-12700H","cx16":true,"lahf-lm":true,"popcnt":true,"pni":true,"ssse3":true,"sse4.1":true,"sse4.2":true}}}}'
        ;;
      esac
      ;;
    *quit*) echo '{"return":{}}'; exit 0 ;;
    esac
  done
  ;;
esac
`
	if err := os.WriteFile(probe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CPU_PROBE_MODE", mode)
	t.Setenv("CPU_PROBE_ARGS", filepath.Join(t.TempDir(), "args"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return probe
}

func allCPUFeatures() map[string]bool {
	return map[string]bool{
		"cx16": true, "lahf-lm": true, "popcnt": true, "pni": true,
		"ssse3": true, "sse4.1": true, "sse4.2": true,
	}
}

func TestCPUContractValidation(t *testing.T) {
	original := expandCPUModel
	t.Cleanup(func() { expandCPUModel = original })

	t.Run("legacy pair skips host probe", func(t *testing.T) {
		called := false
		expandCPUModel = func(context.Context, string) (map[string]bool, error) {
			called = true
			return nil, errors.New("probe should be skipped")
		}
		if err := validateCPU(&config.VM{}); err != nil {
			t.Fatalf("validateCPU(empty pair) = %v, want nil", err)
		}
		if called {
			t.Fatal("legacy empty CPU pair invoked host probe")
		}
	})

	t.Run("supported pair names each missing baseline feature", func(t *testing.T) {
		for _, missing := range []string{"cx16", "lahf-lm", "popcnt", "pni", "ssse3", "sse4.1", "sse4.2"} {
			t.Run(missing, func(t *testing.T) {
				props := allCPUFeatures()
				delete(props, missing)
				expandCPUModel = func(context.Context, string) (map[string]bool, error) {
					return props, nil
				}
				err := validateCPU(&config.VM{CPUModel: CPUModelHost, RequiredCPU: RequiredCPUX8664V2})
				if !errors.Is(err, ErrStartFailed) {
					t.Fatalf("validateCPU missing %s = %v, want ErrStartFailed", missing, err)
				}
				if !strings.Contains(err.Error(), RequiredCPUX8664V2) || !strings.Contains(err.Error(), missing) {
					t.Fatalf("validateCPU missing %s = %v, want baseline and feature", missing, err)
				}
			})
		}
	})

	t.Run("partial and unknown pairs fail before probing", func(t *testing.T) {
		called := 0
		expandCPUModel = func(context.Context, string) (map[string]bool, error) {
			called++
			return allCPUFeatures(), nil
		}
		for _, pair := range []struct{ model, required string }{
			{CPUModelHost, ""}, {"", RequiredCPUX8664V2}, {"qemu64", RequiredCPUX8664V2},
			{CPUModelHost, "x86-64-v3"},
		} {
			err := validateCPU(&config.VM{CPUModel: pair.model, RequiredCPU: pair.required})
			if !errors.Is(err, ErrStartFailed) {
				t.Errorf("validateCPU(%q,%q) = %v, want ErrStartFailed", pair.model, pair.required, err)
			}
		}
		if called != 0 {
			t.Fatalf("invalid CPU pairs invoked host probe %d times", called)
		}
	})
}

func TestCPUProbeQMPProtocolAndFailureBoundaries(t *testing.T) {
	installCPUProbe(t, "success")
	props, err := queryCPUModelExpansion(context.Background(), CPUModelHost)
	if err != nil {
		t.Fatalf("queryCPUModelExpansion success = %v", err)
	}
	for _, feature := range []string{"cx16", "lahf-lm", "popcnt", "pni", "ssse3", "sse4.1", "sse4.2"} {
		if !props[feature] {
			t.Errorf("expanded CPU properties missing %q: %v", feature, props)
		}
	}
	if len(props) != 7 {
		t.Fatalf("expanded CPU properties = %v, want exactly seven flags", props)
	}
	args, err := os.ReadFile(os.Getenv("CPU_PROBE_ARGS"))
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := "-machine pc,accel=kvm -cpu host -smp 1 -nodefaults -display none -S -qmp stdio\n"
	if string(args) != wantArgs {
		t.Fatalf("probe argv = %q, want %q", args, wantArgs)
	}

	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "qmp error object", want: "probe rejected"},
		{name: "malformed reply", want: "invalid character"},
		{name: "missing props", want: "props"},
		{name: "child stderr", want: "qemu/kvm probe stderr sentinel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := map[string]string{
				"qmp error object": "error", "malformed reply": "malformed",
				"missing props": "missing", "child stderr": "stderr",
			}[tc.name]
			installCPUProbe(t, mode)
			_, err := queryCPUModelExpansion(context.Background(), CPUModelHost)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("queryCPUModelExpansion(%s) = %v, want error containing %q", tc.name, err, tc.want)
			}
		})
	}

	t.Run("caller deadline bounds stalled child", func(t *testing.T) {
		installCPUProbe(t, "timeout")
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := queryCPUModelExpansion(ctx, CPUModelHost)
		if err == nil {
			t.Fatal("stalled CPU probe unexpectedly succeeded")
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("stalled CPU probe took %s after caller deadline", elapsed)
		}
	})
}

func TestStartValidatesCPUBeforeMutation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("STOAT_HOME", root)
	dir := filepath.Join(root, "vm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	v := &config.VM{
		Name: "vm", Dir: dir, Mode: "cloud", Backend: "cloudinit", Base: "/missing/base.qcow2",
		Disk: "12G", RAM: 2048, CPUs: 2, CPUModel: CPUModelHost, RequiredCPU: RequiredCPUX8664V2,
	}
	if err := os.WriteFile(v.MonitorPath(), []byte("monitor sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	original := expandCPUModel
	t.Cleanup(func() { expandCPUModel = original })
	expandCPUModel = func(context.Context, string) (map[string]bool, error) {
		props := allCPUFeatures()
		delete(props, "sse4.2")
		return props, nil
	}

	err := Start(v)
	if !errors.Is(err, ErrStartFailed) {
		t.Fatalf("Start() = %v, want ErrStartFailed", err)
	}
	if !strings.Contains(err.Error(), RequiredCPUX8664V2) || !strings.Contains(err.Error(), "sse4.2") {
		t.Fatalf("Start() error = %v, want CPU baseline and missing feature", err)
	}
	if got, readErr := os.ReadFile(v.MonitorPath()); readErr != nil || string(got) != "monitor sentinel" {
		t.Fatalf("monitor sentinel after failed preflight = %q, %v", got, readErr)
	}
	for _, path := range []string{v.DiskPath(), v.OvlDir(), v.WorkDir()} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("preflight failure created %s (stat error %v)", path, statErr)
		}
	}
}
