//go:build linux

package qemu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const cpuProbeTimeout = 5 * time.Second

type cpuProbeMessage struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
	Event string `json:"event"`
}

// queryCPUModelExpansion asks a separate QEMU/KVM process for the effective
// properties exposed by the host CPU model. The child is stopped, diskless,
// and always reaped.
func queryCPUModelExpansion(ctx context.Context, model string) (props map[string]bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, cpuProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, Binary, cpuProbeArgs(model)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("qemu/kvm probe: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("qemu/kvm probe: stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("qemu/kvm probe: start: %w", err)
	}
	probeDone := make(chan struct{})
	go func() {
		select {
		case <-probeCtx.Done():
			// CommandContext stops the direct child, but a shell-backed
			// test binary (and a QEMU wrapper) can leave descendants holding
			// the QMP pipe open. Kill the process group so the deadline bounds
			// the whole probe and not only its immediate child.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		case <-probeDone:
		}
	}()

	waited := false
	defer func() {
		close(probeDone)
		if !waited {
			_ = stdin.Close()
			_ = stdout.Close()
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			waited = true
		}
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				err = fmt.Errorf("%w: %s", err, msg)
			}
			err = fmt.Errorf("qemu/kvm probe: %w", err)
		}
	}()

	dec := json.NewDecoder(stdout)
	var greeting struct {
		QMP json.RawMessage `json:"QMP"`
	}
	if err := dec.Decode(&greeting); err != nil {
		return nil, fmt.Errorf("qmp greeting: %w", err)
	}
	if greeting.QMP == nil {
		return nil, fmt.Errorf("qmp greeting missing QMP object")
	}

	if _, err := cpuProbeCommand(stdin, dec, "qmp_capabilities", nil); err != nil {
		return nil, err
	}
	raw, err := cpuProbeCommand(stdin, dec, "query-cpu-model-expansion", map[string]any{
		"type":  "full",
		"model": map[string]string{"name": model},
	})
	if err != nil {
		return nil, err
	}

	var result struct {
		Model struct {
			Props map[string]json.RawMessage `json:"props"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("query-cpu-model-expansion reply: %w", err)
	}
	if result.Model.Props == nil {
		return nil, fmt.Errorf("query-cpu-model-expansion reply missing model.props")
	}

	props = make(map[string]bool)
	for name, value := range result.Model.Props {
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err == nil {
			props[name] = enabled
		}
	}

	if _, err := cpuProbeCommand(stdin, dec, "quit", nil); err != nil {
		return nil, err
	}
	if err := stdin.Close(); err != nil {
		return nil, fmt.Errorf("qmp quit: close stdin: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		waited = true
		return nil, fmt.Errorf("wait: %w", err)
	}
	waited = true
	return props, nil
}

func cpuProbeCommand(stdin io.Writer, dec *json.Decoder, name string, args map[string]any) (json.RawMessage, error) {
	request := map[string]any{"execute": name}
	if args != nil {
		request["arguments"] = args
	}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		return nil, fmt.Errorf("qmp %s: write: %w", name, err)
	}
	for {
		var response cpuProbeMessage
		if err := dec.Decode(&response); err != nil {
			return nil, fmt.Errorf("qmp %s: %w", name, err)
		}
		if response.Event != "" {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("qmp %s: %s", name, response.Error.Desc)
		}
		if response.Return == nil {
			return nil, fmt.Errorf("qmp %s: reply missing return", name)
		}
		return response.Return, nil
	}
}
