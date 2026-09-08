package fake

import (
	"context"
	"testing"

	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/provider"
)

func TestInstallReplacesQemuForOneTest(t *testing.T) {
	before, err := provider.For(&config.VM{})
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}

	t.Run("installed", func(t *testing.T) {
		f := Install(t)
		f.SetRunning("dev")
		p, err := provider.For(&config.VM{Name: "dev"})
		if err != nil {
			t.Fatalf("For() error = %v", err)
		}
		if p != provider.Provider(f) {
			t.Fatalf("For() = %T, want the installed fake", p)
		}
		s, err := p.Status(context.Background(), &config.VM{Name: "dev"})
		if err != nil {
			t.Fatalf("Status() error = %v", err)
		}
		if !s.Running {
			t.Error("Running = false, want true: the fake was told this VM runs")
		}
	})

	after, err := provider.For(&config.VM{})
	if err != nil {
		t.Fatalf("For() error = %v", err)
	}
	if after != before {
		t.Errorf("provider after the subtest = %T, want the qemu provider restored", after)
	}
}
