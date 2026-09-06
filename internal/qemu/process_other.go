//go:build !linux

package qemu

import (
	"time"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/hostops"
)

// pid is kept side-effect free on hosts without a qualified process adapter.
func pid(*config.VM) int { return 0 }

// Running does not remove stale state on an unqualified host. qemu.Start and
// qemu.Stop reject before reaching this read path.
func Running(*config.VM) bool { return false }

func StartedAt(*config.VM) time.Time { return time.Time{} }

func terminate(int) error { return hostops.ErrUnsupported }
