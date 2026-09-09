// Package gce implements the Provider interface over the Compute Engine API.
package gce

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"

	"github.com/novusedge/stoat/internal/backend"
	"github.com/novusedge/stoat/internal/capabilities"
	"github.com/novusedge/stoat/internal/cloudinit"
	"github.com/novusedge/stoat/internal/config"
	"github.com/novusedge/stoat/internal/guest"
	"github.com/novusedge/stoat/internal/iso"
	"github.com/novusedge/stoat/internal/keys"
	"github.com/novusedge/stoat/internal/provider"
	"github.com/novusedge/stoat/internal/settings"
	"github.com/novusedge/stoat/internal/sshx"
)

func init() {
	provider.Register("gce", Provider{})
}

// Provider is one Compute Engine instance per stoat VM.
type Provider struct{}

// unsupported lists what this provider does not implement over the local
// QEMU surface: everything that assumes a hypervisor process on this host
// (a VNC framebuffer for share/screenshot/sendkey, a console log file) or
// a disk stoat can clone or snapshot directly. update's ram and cpu edits
// are unsupported too: they resize a running qemu process's allocation,
// which has no equivalent on a Compute Engine instance short of a stop,
// SetMachineResources, and restart this provider does not yet implement.
var unsupported = []string{
	"share", "screenshot", "sendkey", "console_log", "forward",
	"snapshot", "clone", "update.ram", "update.cpu",
}

func (Provider) Name() string { return "gce" }

func (Provider) Capabilities(*config.VM) []capabilities.Capability {
	out := make([]capabilities.Capability, len(unsupported))
	for i, name := range unsupported {
		out[i] = capabilities.Capability{
			Name:   name,
			Status: capabilities.StatusUnsupported,
			Reason: &capabilities.Reason{Code: capabilities.ReasonProviderUnsupported},
		}
	}
	return out
}

// vmSettings resolves the credentials config.toml carries plus the
// project and zone v was created in. It never re-runs settings.ResolveGCE:
// that precedence (flag, config.toml, gcloud) applies once, at create time.
func vmSettings(v *config.VM) (settings.GCE, error) {
	cfg, err := settings.Load()
	if err != nil {
		return settings.GCE{}, err
	}
	gce := cfg.Providers.GCE
	if v.GCEProject == "" || v.GCEZone == "" {
		return settings.GCE{}, fmt.Errorf("gce: %s carries no project/zone; it was not created by this provider", v.Name)
	}
	gce.Project = v.GCEProject
	gce.Zone = v.GCEZone
	return gce, nil
}

// Create builds the firewall rule first, then the instance, so the instance
// never comes up reachable-then-locked-down. A failed insert deletes the
// rule it already created; core has not written vm.toml as created yet, so
// a caller retrying Create must not find an orphaned rule from this attempt.
func (Provider) Create(ctx context.Context, v *config.VM) error {
	s, err := vmSettings(v)
	if err != nil {
		return err
	}
	image, err := iso.GCEImageForOS(v.OS)
	if err != nil {
		return err
	}
	seed, err := buildSeed(v)
	if err != nil {
		return err
	}
	sourceRange, err := sourceRangeFor(ctx, s)
	if err != nil {
		return fmt.Errorf("gce: %w", err)
	}

	fwClient, err := newFirewallsClient(ctx, s)
	if err != nil {
		return err
	}
	defer fwClient.Close()

	fwOp, err := fwClient.Insert(ctx, &computepb.InsertFirewallRequest{
		Project:          s.Project,
		FirewallResource: firewallRule(v.Name, sourceRange),
	})
	if err != nil {
		return fmt.Errorf("gce: creating firewall rule: %w", err)
	}
	if err := fwOp.Wait(ctx); err != nil {
		return fmt.Errorf("gce: creating firewall rule: %w", err)
	}

	req, err := insertRequest(v, s, image, seed, sourceRange)
	if err != nil {
		_ = deleteFirewallRule(ctx, fwClient, s, v.Name)
		return err
	}

	instClient, err := newClient(ctx, s)
	if err != nil {
		_ = deleteFirewallRule(ctx, fwClient, s, v.Name)
		return err
	}
	defer instClient.Close()

	insOp, err := instClient.Insert(ctx, req)
	if err != nil {
		_ = deleteFirewallRule(ctx, fwClient, s, v.Name)
		return fmt.Errorf("gce: creating instance: %w", err)
	}
	if err := insOp.Wait(ctx); err != nil {
		_ = deleteFirewallRule(ctx, fwClient, s, v.Name)
		return fmt.Errorf("gce: creating instance: %w", err)
	}
	return nil
}

func deleteFirewallRule(ctx context.Context, c *compute.FirewallsClient, s settings.GCE, vm string) error {
	op, err := c.Delete(ctx, &computepb.DeleteFirewallRequest{Project: s.Project, Firewall: firewallName(vm)})
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

// buildSeed renders the same cloud-init user-data a QEMU cloud VM gets, as
// a plain string for instance metadata: gce has no cdrom device to attach a
// NoCloud ISO to.
func buildSeed(v *config.VM) (string, error) {
	if err := keys.Ensure(); err != nil {
		return "", err
	}
	pub, err := keys.PublicKey()
	if err != nil {
		return "", err
	}
	scripts, err := backend.RecipeScripts(v)
	if err != nil {
		return "", err
	}
	var prelude string
	if o, ok := guest.Lookup(v.OS); ok {
		prelude = guest.Prelude(o, "sh")
	}
	var bodies []string
	if frag := cloudinit.WrapScripts(scripts, prelude); frag != "" {
		bodies = []string{frag}
	}
	return cloudinit.UserData(v, pub, bodies)
}

func (Provider) Start(ctx context.Context, v *config.VM) error {
	s, err := vmSettings(v)
	if err != nil {
		return err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	op, err := c.Start(ctx, &computepb.StartInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return fmt.Errorf("gce: starting %s: %w", v.Name, err)
	}
	return op.Wait(ctx)
}

func (Provider) Stop(ctx context.Context, v *config.VM) error {
	s, err := vmSettings(v)
	if err != nil {
		return err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	op, err := c.Stop(ctx, &computepb.StopInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return fmt.Errorf("gce: stopping %s: %w", v.Name, err)
	}
	return op.Wait(ctx)
}

// Destroy deletes the instance, then its firewall rule, then returns. core
// deletes vm.toml only after this returns nil (Provider's own contract);
// deleting the firewall rule before the instance would leave the instance
// briefly reachable from nowhere and briefly unreachable from everywhere,
// for no benefit.
func (Provider) Destroy(ctx context.Context, v *config.VM) error {
	s, err := vmSettings(v)
	if err != nil {
		return err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	op, err := c.Delete(ctx, &computepb.DeleteInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return fmt.Errorf("gce: deleting %s: %w", v.Name, err)
	}
	if err := op.Wait(ctx); err != nil {
		return fmt.Errorf("gce: deleting %s: %w", v.Name, err)
	}

	fwClient, err := newFirewallsClient(ctx, s)
	if err != nil {
		return err
	}
	defer fwClient.Close()
	return deleteFirewallRule(ctx, fwClient, s, v.Name)
}

// running is GCE's own set of statuses that mean stoat can reach the guest.
// PROVISIONING and STAGING count too: neither exits Status() as "stopped"
// while the instance is on its way up. REPAIRING falls to the default
// (not running): the API document does not say whether the guest is
// reachable during repair, so a caller waiting for "running" should keep
// waiting rather than be told the instance already came up.
var running = map[string]bool{
	"PROVISIONING": true,
	"STAGING":      true,
	"RUNNING":      true,
	"STOPPING":     false,
	"SUSPENDING":   false,
	"SUSPENDED":    false,
	"TERMINATED":   false,
}

func (Provider) Status(ctx context.Context, v *config.VM) (provider.Status, error) {
	s, err := vmSettings(v)
	if err != nil {
		return provider.Status{}, err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return provider.Status{}, err
	}
	defer c.Close()
	return statusFor(ctx, c, s, v)
}

// statusFor takes an already-built client so a test can hand it one wired
// to a fake http.RoundTripper instead of real credentials.
func statusFor(ctx context.Context, c *compute.InstancesClient, s settings.GCE, v *config.VM) (provider.Status, error) {
	inst, err := c.Get(ctx, &computepb.GetInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return provider.Status{}, fmt.Errorf("gce: getting %s: %w", v.Name, err)
	}
	raw := inst.GetStatus()
	return provider.Status{Running: running[raw], Raw: raw}, nil
}

// Endpoint pins the host key to a per-VM file: this connection crosses a
// routable network, where sshx's loopback "skip checking" policy would let
// a machine-in-the-middle intercept it silently.
func (Provider) Endpoint(ctx context.Context, v *config.VM) (sshx.Endpoint, error) {
	s, err := vmSettings(v)
	if err != nil {
		return sshx.Endpoint{}, err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return sshx.Endpoint{}, err
	}
	defer c.Close()
	return endpointFor(ctx, c, s, v)
}

func endpointFor(ctx context.Context, c *compute.InstancesClient, s settings.GCE, v *config.VM) (sshx.Endpoint, error) {
	inst, err := c.Get(ctx, &computepb.GetInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return sshx.Endpoint{}, fmt.Errorf("gce: getting %s: %w", v.Name, err)
	}
	addr := externalAddress(inst)
	if addr == "" {
		return sshx.Endpoint{}, fmt.Errorf("gce: %s has no external address", v.Name)
	}
	return sshx.Endpoint{
		Name:       v.Name,
		Host:       addr,
		Port:       22,
		User:       sshx.User(v),
		KnownHosts: filepath.Join(v.Dir, "known_hosts"),
	}, nil
}

// Details reports the facts CLI and TUI show beyond Status: project, zone,
// machine type, external address and both stop deadlines.
func (Provider) Details(ctx context.Context, v *config.VM) (provider.Details, error) {
	s, err := vmSettings(v)
	if err != nil {
		return provider.Details{}, err
	}
	c, err := newClient(ctx, s)
	if err != nil {
		return provider.Details{}, err
	}
	defer c.Close()
	inst, err := c.Get(ctx, &computepb.GetInstanceRequest{Project: s.Project, Zone: s.Zone, Instance: v.Name})
	if err != nil {
		return provider.Details{}, fmt.Errorf("gce: getting %s: %w", v.Name, err)
	}
	hard, _ := hardDeadline(inst)
	soft, _ := softDeadline(inst.GetLabels())
	return provider.Details{
		Project:      s.Project,
		Zone:         s.Zone,
		MachineType:  path.Base(inst.GetMachineType()),
		Address:      externalAddress(inst),
		HardDeadline: hard,
		SoftDeadline: soft,
	}, nil
}

func externalAddress(inst *computepb.Instance) string {
	for _, ni := range inst.GetNetworkInterfaces() {
		for _, ac := range ni.GetAccessConfigs() {
			if ac.GetNatIP() != "" {
				return ac.GetNatIP()
			}
		}
	}
	return ""
}
