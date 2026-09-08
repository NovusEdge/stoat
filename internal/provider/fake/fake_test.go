package fake

import (
	"context"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
)

func TestInstallReplacesQemuForOneTest(t *testing.T) {
	f := Install(t)
	f.SetRunning("dev")

	p, err := provider.For(&config.VM{Name: "dev"})
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	s, err := p.Status(context.Background(), &config.VM{Name: "dev"})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !s.Running {
		t.Error("Running = false, want true: the fake was told this VM runs")
	}
}
