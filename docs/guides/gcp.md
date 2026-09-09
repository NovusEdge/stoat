# The GCP path

Stoat can run a VM as a Google Compute Engine instance instead of local
QEMU. Use it when you need a machine reachable from outside your own
network, or one that outlives your laptop being closed.

## What it gives you, and what it costs

You get a real instance with a public address, reachable over SSH from
wherever you are. It keeps running (and billing) after you close your
laptop, until it hits its run-time limit or you stop it.

Three things cost money, separately from stoat and from each other:

- The instance, while it runs.
- Its boot disk, while the instance is stopped. Stopping does not delete
  the disk, and the disk keeps billing.
- The external (public) IP address.

Check current rates for the machine type, disk size, and region you pick;
this guide does not quote prices, because they change.

## Authenticate

Stoat does not manage a GCP credential of its own. It uses Application
Default Credentials (ADC), the same ones `gcloud` and Google's client
libraries read:

```sh
gcloud auth application-default login
```

This opens a browser and writes a credential file `gcloud` and stoat both
read. Run it once per machine. `providers.gce.service_account_key_file` in
`config.toml` is an escape hatch for an environment that can't produce ADC
or use impersonation; leave it unset otherwise.

## Write `~/.stoat/config.toml`

`config.toml` holds account-wide GCP settings; nothing here is duplicated
into `vm.toml`.

```toml
[providers.gce]
project = "engrammic"
zone = "europe-west4-a"
```

Both fields are optional here: a missing one falls back to `gcloud`'s
active configuration (`gcloud config set project ...` /
`gcloud config set compute/zone ...`), and a `stoat create` flag overrides
both. `providers.gce.project` also gates `stoat prune`'s remote pass: with
it unset, prune never calls the GCP API.

Two more fields matter later:

```toml
[providers.gce]
project = "engrammic"
zone = "europe-west4-a"
max_run_duration = "8h"
source_range = "203.0.113.4/32"
```

`max_run_duration` bounds how long an instance runs before GCP stops it
(default `24h`). It's set once, at creation, and never moved afterward:
changing it needs a stopped instance, so it can't reset the deadline of a
VM you meant to keep running. `source_range` is covered under
[Firewall and your address](#firewall-and-your-address) below.

## Create and use a VM

```sh
stoat create cloudy --image ubuntu-24.04 --provider gce
```

```
created cloudy (ubuntu, cloud, ssh port 22)
gcp project engrammic, zone europe-west4-a, from ~/.stoat/config.toml
start it with: stoat up cloudy
```

`--gcp-project` and `--gcp-zone` override `config.toml` and `gcloud` for
this one VM; the second output line names whichever source actually
supplied the values. `create` only writes the record; nothing on GCP
exists yet.

```sh
stoat up cloudy
stoat ssh cloudy
```

`up` inserts the instance and its firewall rule on first start, then waits
for SSH. Later starts restart the same instance, so its boot disk and
whatever you put on it survive a `stoat down` / `stoat up` cycle.

`stoat get cloudy` shows the GCP-specific fields: project, zone, machine
type, external address, and `expires` (the nearer of the run-time limit
and any operator-requested stop).

## Deadlines, and extending one

Every instance has a **hard deadline**: `max_run_duration` after it last
started, enforced by GCP itself. GCP stops the instance when it's reached;
stoat cannot prevent that, only warn about it. It also carries a **soft
deadline**, a label stoat writes and can move on its own.

Within an hour of either deadline, `stoat ls` and any command touching
that VM print a warning:

```
cloudy: stops in 42m (run-time limit). extend with: stoat gce extend cloudy 4h
```

Move the soft deadline forward without stopping the instance:

```sh
stoat gce extend cloudy 4h
```

```
cloudy: soft deadline now 2026-09-09T18:04:00Z (in 4h0m0s)
```

This only works up to the hard deadline. `max_run_duration` itself can't
move while the instance runs (GCP's `setScheduling` requires a stopped
instance), so extending past the hard deadline is refused, naming it and
saying a stop and start resets it:

```
gce: cloudy's run-time limit is 2026-09-09T20:00:00Z; extending past it needs a stop and start to reset that limit first
```

## What `prune` reports

With `providers.gce.project` set, `stoat prune` also compares GCP's
stoat-owned instances against your local `vm.toml` records, and reports
three kinds of mismatch:

- `orphan`: GCP holds an instance stoat's own label marks as its own, but
  there's no local record for it — usually a `vm.toml` lost from this
  machine.
- `stale`: a local record exists, but GCP has no such instance — it was
  deleted outside stoat, in the console or by another machine.
- `stopped`: both agree, and the instance has been stopped a while.  Its
  disk is still billing.

`prune` only ever reports `orphan` and `stopped` entries; it never deletes
a remote instance on a sweep. Only a `stale` local record is removable,
and only with `--apply`. For `orphan` or `stopped`, the report names
`stoat rm` as the next step, which you run once you've decided the
instance really is disposable.

## Firewall and your address

Every instance gets its own firewall rule admitting SSH (tcp/22) from one
source range, scoped to your address at the moment stoat looked it up — no
one else's traffic reaches the instance. With no `source_range` set,
stoat asks an echo service what address the request came from and locks
the rule to that single address.

If your address changes (a new network, a new day on a dynamic IP), SSH to
existing VMs stops working: the firewall rule now excludes you. Set
`providers.gce.source_range` to a CIDR that covers where you actually
connect from, then recreate the VM (or wait for the next `stoat create`)
to pick it up. A split-tunnel VPN or a proxy is exactly the case where the
automatic lookup gets the wrong address, since your SSH traffic and your
HTTPS traffic can leave by different paths.

The guest's host key is pinned to a file under the VM's own directory the
first time stoat connects, the same as any other host you `ssh` to for the
first time. A rebuilt instance with a new host key will need that entry
cleared before stoat can reconnect.

## What doesn't work on GCE, and why

**Debian is not a supported guest here.** Ubuntu is the only supported
guest for the GCE path. Google's official Debian GCE images carry no
cloud-init, so stoat's seed — the mechanism that installs the SSH key,
creates the `stoat` user, and runs recipes on first boot — never runs.
`stoat create --provider gce` with a Debian image is refused before any
instance is created: `no GCE image for this OS`.

A GCE instance also has no equivalent of these local, QEMU-specific
operations: `screenshot`, sending a key to the console, tailing a console
log, mounted host shares, snapshots, `clone`, port `forward`, and resizing
CPU or RAM on a running instance. `stoat capabilities` on a gce VM reports
each of these as unsupported, with the reason.
