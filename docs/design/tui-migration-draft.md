# Migrating internal/tui onto internal/core

Planning document. No code was changed. Written against the working tree of
2026-08-04, which has a large in-flight comment sweep on top of `d940ff9`
plus `c3ba57b` (sshx ctx).

## 0. Blocking precondition

`internal/tui` does not compile in the working tree right now:

```
internal/tui/autoprov.go:30:26: not enough arguments in call to sshx.Wait
internal/tui/ssh.go:47:21:      not enough arguments in call to sshx.Wait
```

`sshx.Wait` gained a leading `context.Context` in the working tree (it is
`func Wait(v *config.VM, timeout time.Duration) error` at HEAD, and
`func Wait(ctx, v, timeout)` in the tree). The two TUI callers were not
updated. HEAD itself is fine, so this is an uncommitted in-flight change, not
a committed regression. Nothing below can be verified green until it is
fixed. Whoever owns the sshx change owns those two lines.

---

## 1. Drift inventory

§9.6 names seven things that move. Each is located below, with the core call
that replaces it. Some have already moved; those are recorded as done so the
remaining work is not overstated.

### 1.1 Image resolution and download state: partly moved

| TUI site | What it does | core replacement |
|---|---|---|
| `form.go:170 buildImages()` | two-pass catalog-then-BYO assembly, download flag, exact-vs-declared size | `core.Images()` (`images.go:83`), field for field |
| `form.go:208 imageBytes()` | `os.Stat` under `isos/` | `core.fileSize` (`images.go:64`), unexported |
| `form.go:133 localImageFiles()` | `core.LocalImages()` + sort | already core; only the sort is TUI's |
| `form.go:165 matchLocalImage` | `var matchLocalImage = core.MatchLocal` | already core |
| `form.go:143 byoOptionFromPath()` | stat, refuse dir, `iso.Infer` | `core.resolveImage`'s absolute branch (`image.go:119-129`), same three steps, unexported |
| `form.go:497 fetchImage()` | `iso.Resolve` + `iso.Download(context.Background(), …)` | `core.DownloadImage(ctx, id, progress)` (`images.go:145`) |
| `form.go:70 imagePath()` | absolute-vs-`isos/` join | `core.image.isoField()` (`image.go:103`), inverse of the same rule |

`buildImages` is a straight duplicate of `core.Images`. The two cannot
currently disagree about *which file satisfies which entry* (both call
`core.MatchLocal`), but they each decide independently that a downloaded
file's stat size supersedes the catalog's declared size, and each carries its
own copy of that comment.

`fetchImage` is the one with a live consequence: it passes
`context.Background()`, so `esc` during a download leaves the goroutine
running. `app.go:328`'s own comment names this as a known open item, and
`core.DownloadImage`'s doc says it is fixed there.

### 1.2 OS/backend derivation: partly moved (create), not moved (edit)

Create path: `formModel.resolvedBackend()` (`form.go:364`),
`resolvedOS()` (`:378`), `resolvedSSHUser()` (`:389`),
`effectiveMode()` (`:414`).

- `resolvedBackend`/`resolvedOS` are the BYO override fold. core does the
  same in `image.apply()` (`image.go:170`).
- `effectiveMode` is `core.modeFor()` (`core.go:252`) written again:
  cloudinit→cloud, ssh→disk, else the user's live/disk choice.
- `resolvedSSHUser` is `image.apply`'s `cloudinit.User` rule written again,
  and it is **dead production code**. `spec()` (`form.go:720`) never calls
  it; `core.Spec` has no SSHUser field. Its only remaining caller is
  `creds_test.go`'s `TestBrowsedByoCloudinitResolvesSSHUser`.

Edit path, nothing moved: `backendOf(v)` (`edit.go:132`) and
`backendForMode(v, mode)` (`edit.go:118`). core has no equivalent because
core holds Backend immutable. See divergence D3.

### 1.3 Port allocation: moved (create), not moved (edit)

Create no longer allocates; `core.plan` calls `config.FreePort()` under
`config.Lock()` (`core.go:95`, `:203`).

Edit still validates a hand-typed port itself: `edit.go:283-298` plus
`brokenPortHolder` (`edit.go:98`). Replacement is `core.Update`'s
`validateSSHPort` (`update.go:225`). They disagree; see D1.

### 1.4 vm.toml construction: moved (create), not moved (edit or detail)

- Create: `spec()` → `core.Plan` → `core.Create`. Done.
- Edit: `validate()` builds `next := *e.vm` and mutates it (`edit.go:300-357`);
  `saveEdit()` calls `v.Save()` directly (`edit.go:417`). No lock. Replacement
  is `core.Update(name, core.Patch{…})`.
- Detail `i` key: `detail.go:112-117` copies the VM, flips `Installed`, and
  calls `next.Save()`. A second unlocked writer to vm.toml, with **no core
  equivalent at all**: `core.Patch` has no `Installed` field.
- Detail `E` key: `$EDITOR` on vm.toml then `config.Load` (`detail.go:89-101`).
  Legitimately raw, but it takes no lock either, and it is the one path that
  can make the vm.toml `name` field diverge from the directory (see D15).

### 1.5 Disk creation: moved (create), not moved (edit)

`saveEdit` (`edit.go:375-416`) runs both `qemu-img create` (for a live→disk
mode switch) and `qemu-img resize`. `core.Update` does the resize
(`update.go:182`) with the same before-the-write ordering, and has **no**
create path, because it refuses the mode change that would need one.

### 1.6 The provisioning decision: not moved

| TUI site | core replacement |
|---|---|
| `provision.go:35 provision()` → `sshx.Provision(context.Background(), v)` | `core.Apply(ctx, name, core.ApplyOpts{})` (`apply.go:75`) |
| `provision.go:57 startProvision()` refusals | partly `core.Apply`'s; see D9 |
| `autoprov.go:47 wantsAutoProvisionPrompt()` | none, and correctly none (see §3) |
| `autoprov.go:72 lastProvisionSucceeded()` | `core.lastProvisionLineIs(v,"done")` (`wait.go:145`), unexported |
| `autoprov.go:101 ensureNoStaleLog()` | none (see §3; this one should move) |

### 1.7 Readiness / progress state machine: not moved

| TUI site | core replacement |
|---|---|
| `autoprov.go:27 awaitSSH()` → `sshx.Wait(v, 90s)` | `core.Wait(ctx, name, core.UntilReachable)` (`wait.go:87`) |
| `ssh.go:47` → `sshx.Wait(v, 800ms)` | same, with a short ctx |
| `cloudinit.go:46 checkCloudInit()` | **nothing**: core has no cloud-init readiness |
| `provstep.go:65 readProvStep()` | none needed; stays (see §3) |
| `list.go:38 startVM` → `qemu.Start` | `core.Start` (`vm.go:262`) |
| `list.go:54 stopVM` → `qemu.Stop` | `core.Stop` (`vm.go:277`) |
| `list.go:209 deleteVM` → `v.Delete()` | `core.Destroy` (`vm.go:304`) |
| `list.go:223 deleteBrokenVM` | `core.Destroy`, same call, broken branch |
| `app.go:90 loadVMs` → `config.List` + `config.ListBroken` | `core.List()` (`vm.go:208`), which does the merge |

---

## 2. Divergences

Where the TUI's copy of a rule and core's copy disagree. This is the section
that names live bugs.

### D1. SSH port validation. Live bug.

`edit.go:283-298` accepts a port if:
- it parses and is in 1024–65535,
- no VM in `others` (which is `m.vms`, the last `loadVMs` snapshot) has that
  `SSHPort`,
- no broken VM's regexed `sshport` is that port.

`core.Update`'s `validateSSHPort` (`update.go:225`) → `validateForwards`
(`forward.go:60`) additionally refuses:
- a port matching one of **this** VM's own declared forwards,
- a port matching **any other VM's declared forwards**,

and it does the whole read-modify-write under `config.Lock()`, reading
`config.List()` fresh rather than a UI snapshot.

Consequence: the TUI will happily set VM A's ssh port to a host port VM B has
declared as a forward. Both then bind `127.0.0.1:<port>`; the second qemu
fails at start with a message naming neither VM nor port. `stoat forward`
refuses the mirror of this; the edit screen does not. The reason nobody has
hit it is that **forwards are not visible anywhere in the TUI**, which is
also why exposing them (§4) is worth more than it looks.

Secondary: validating against `m.vms` means a VM created by another process
since the last refresh is invisible to the check.

### D2. Disk grow. Live bug, data-destroying.

`edit.go:334-345`:

```go
oldBytes, err1 := parseSize(e.vm.Disk)
newBytes, err2 := parseSize(size)
if err2 != nil { return nil, fmt.Errorf("disk size: %v", err2) }
if err1 == nil && newBytes < oldBytes { return nil, ...shrink... }
out.resizeTo = size
```

If the VM's **current** size fails to parse, `err1 != nil`, the shrink check
is skipped entirely, and the resize proceeds. `core.validateDiskGrow`
(`update.go:278-293`) refuses outright in that case:
`"disk: current size %q: %v"`.

The unparseable-current-size case is not hypothetical: `ParseSize`'s own
comment (`core.go:283-289`) describes a release that wrote `disk = "+8G"`
into vm.toml. Such a VM can be edited down to `1G` in the TUI, which runs
`qemu-img resize disk.qcow2 1G` and truncates the image. core refuses.

Other differences on the same field, non-bugs but worth stating:
- edit.go refuses a resize-while-running at **save** time (`edit.go:403`,
  after enter, as an `errMsg` toast). core refuses at **validation** time
  with `ErrAlreadyRunning`. Same outcome, different place, different surface
  (toast vs inline field error).
- edit.go demands a non-empty size in disk mode ("disk size is required in
  disk mode", `edit.go:331`). `core.Patch.Disk == nil` simply means "don't
  touch", so Update never demands one. Different shapes, not a conflict.
- Both skip the comparison when the *current* size is empty. Agree.

### D3. Mode is editable in the TUI and immutable in core.

`edit.go:58` offers `live | disk | cloud`; `validate` writes `next.Mode`,
derives `next.Backend = backendForMode(...)`, and `saveEdit` will even
`qemu-img create` a disk to make a live→disk switch work.

`core.checkImmutable` (`update.go:105-118`) returns `ErrImmutableField` for
any change to Name, OS, Backend **or Mode**.

core's own comment (`update.go:88-104`) says this is a knowing narrowing of
`core-api.md` §2.1, which says mode is immutable only "after first boot",
because nothing in the codebase can answer "has this VM booted" for all three
modes.

So the design doc's stated rule ("the API must not be looser than the TUI",
§2.1) is inverted here: core is stricter, and the TUI is the loose one. A
mechanical migration of edit.go onto `core.Update` **removes the mode
switch**. That is a user-visible feature removal and the single largest
judgement call in this migration.

Downstream of D3: the TUI's mode switch is what can produce a VM with
`mode = "disk"` and `backend = "cloudinit"`, which is the state D9a turns
into a second bug.

### D4. Backend derivation for a VM with an empty `backend` field.

`edit.go:132 backendOf(v)`: empty backend → `live` means apkovl, anything
else means ssh.

`internal/backend.For(v)` (`backend.go:65-79`): empty backend → look the OS
up in `guest` and use `guest.OS.Backend`; unknown → `noop{}`.

These disagree for a pre-backend-field VM whose OS is known: a `mode="disk"`
Alpine VM gets `ssh` from `backendOf` and `apkovl` from `backend.For`. The
consequence is which recipes the edit screen offers versus which backend
actually runs them. `core.fromConfig` reports `v.Backend` raw (possibly
`""`), so a TUI reading `core.VM.Backend` would get a third answer: empty.

I did not find a VM in the tree that exercises this, so I am flagging it as a
real inconsistency with an unverified blast radius, not a confirmed bug.

### D5. "Is this VM broken", and what may be done to it. Live bug.

TUI: `config.ListBroken()` → a second item kind (`vmlist.go:20 vmItem.broken`),
every action special-cased. `list.go:223 deleteBrokenVM` reimplements the
data-root containment check and calls `os.RemoveAll`. It **never checks
whether a qemu process is still running against that directory.**

`core.Destroy` (`vm.go:317-336`) handles exactly this case and explicitly
does check, with a comment describing the bug it caused: a vm.toml corrupted
*after* its VM was started made Destroy "a quiet backdoor around the refusal
below, deleting the directory, pidfile, monitor socket and disk, out from
under a live qemu."

The TUI still has that backdoor. It is one keypress (`d`, `y`) on a broken
row.

Also: core models broken as `StateBroken` on the same `VM` type, so a caller
gets one list and one type. The TUI keeps two slices and two code paths.

### D6. "Is this VM running." Agree.

Both use `qemu.Running`. The only difference is cost: the TUI calls it inside
`Render` for every row every frame plus in `viewDetail` and `viewEdit`, core
calls it once per `fromConfig`.

### D7. "Is this VM reachable." Two implementations, both current.

- TUI: `sshx.Wait` (TCP + `SSH-` banner, fixed timeout).
- core: `waitReachable` → `sshBannerUp` (`wait.go:87-113`), a ctx-aware
  reimplementation of the same check, because `sshx.Wait` takes a fixed
  timeout and cannot be cancelled mid-dial.

core's comment states this openly. It is nonetheless a second copy of a
readiness rule, which is precisely §1(a) of the guest-subsystem design. Now
that `sshx.Wait` has grown a ctx in the working tree (§0), the stated reason
for the duplicate may no longer hold; worth checking before the TUI is
pointed at either one.

Behavioural difference to preserve on migration: `core.Wait(UntilReachable)`
refuses a not-running VM with `ErrCannotReach`; `awaitSSH` returns `nil`
silently on any failure, and `autoprov.go:22-26` explains why that silence is
deliberate.

### D8. "Did the last apply succeed." Same function, written twice.

`autoprov.go:72 lastProvisionSucceeded` and `core.lastProvisionLineIs`
(`wait.go:145`) both read the apply log and compare the last non-blank line
to `"done"`. They agree.

Difference: the TUI reads the last 8 KiB (`tailBytes(…, provTailBytes)`);
core `os.ReadFile`s the whole thing. Same answer, different cost on the
hundreds-of-KB log a desktop recipe produces.

### D9. The provisioning refusals.

`startProvision` (`provision.go:57`) refuses, in order: `Mode == "cloud"`;
`Mode == "disk" && !Installed`; zero recipes; already in flight.

`core.Apply` (`apply.go:75`) refuses, in order: not found/broken; not
running; `backend.For(v).Name() == "cloudinit"`; a name in `Only` not on the
VM. Zero targets returns `nil`.

Four disagreements:

**a. The cloud refusal keys on different fields.** TUI: `v.Mode == "cloud"`.
core: the *backend*. For a VM with `mode="disk"` and `backend="cloudinit"`
(reachable through the TUI's own mode switch, D3), the TUI **allows**
provisioning and `sshx.Provision` pipes a `#cloud-config` YAML document into
`sh -s` over ssh. core refuses with `ErrAppliedAtBoot`, and its doc comment
says that is exactly why the error is typed. The TUI's edit screen creates
the state its own provision path mishandles.

**b. "Not installed yet" has no core equivalent.** `core.Apply` on an
uninstalled disk VM will ssh into the installer's tmpfs and run recipes
against a root that is about to be replaced. The TUI's guard
(`provision.go:70`) is the only thing preventing that, on either surface.
Migrating must keep it in front of the call, not fold it in.

**c. Zero recipes.** TUI: an error toast, "no recipes selected, nothing to
provision". core: returns `nil`, i.e. success. `provision.go:51-56`'s comment
records that "reported provisioned for having done nothing" was already fixed
once here. A naive migration reintroduces it.

**d. Not running.** TUI checks inside `provision()` (`:37`) and returns
`errNotRunning`; core checks in `Apply` and returns `ErrNotRunning`. Agree.

### D10. Create-form defaults vs core.Plan's.

Agreeing: RAM 4096 (`form.go:455` / `core.DefaultRAM`), CPUs 4, disk `8G`,
mode `live`, and the cloudinit-only console password.

Disagreeing:

**a. Disk on a live VM.** `order()` (`form.go:323`) omits the disk row in
live mode, but `spec()` (`form.go:768`) reads
`f.inputs[fDisk].Value()` **unconditionally**, so it always sends `"8G"`.
`core.Plan` only defaults a disk when `mode != "live"` (`core.go:190`), and
accepts but never uses a non-empty one. Result: a live VM created in the TUI
records `disk = "8G"`; the same VM created by `stoat create --mode live`
records `disk = ""`. Cosmetic in isolation, but it is exactly the
TUI-and-CLI-produce-different-VMs class that `core.go:182-186`'s comment says
this package exists to remove, and it interacts with D2 and with
`validateDiskGrow`'s live refusal.

**b. Generated names.** `core-api.md` §8 decision 4 specifies an empty name
generating a word pair (`brisk-otter`). Neither the form nor `core.plan`
implements it; `plan` returns `ErrInvalidSpec: name is required`. Not a
divergence between the two; it is a documented feature absent from both.

**c. Numeric field messages.** The form pre-parses RAM/CPUs and emits "ram
must be a number of MB, at least 256" for a **parse** failure; core emits the
same sentence only for a value **below 256** (`core.go:157`). The wording
matches by coincidence, not construction.

**d. Share and `~`.** The form pre-fills `~/vms`; `spec()` passes it
verbatim; `core.Create` writes it verbatim. `config.Load` expands `~` on read
(`config.go:175`), and `qemu.Args` uses `v.Share` in an argv with no shell
(`args.go:81`), so the default does work. But: any edit round-trip reads the
**expanded** value and writes it back absolute, so editing any VM silently
rewrites `share = "~/vms"` to `share = "/home/u/vms"`. That is true of both
`edit.go` and any future `core.Update` caller that patches Share from a
`core.VM`. It is a shared quirk of the read-expands/write-verbatim
asymmetry, not a TUI-vs-core disagreement, but it will surface during Phase 4
and is worth deciding about rather than rediscovering.

### D11. Recipe listing: three call sites, one rule.

`form.go:429 refreshRecipes` → `recipes.List(resolvedOS(), resolvedBackend())`.
`edit.go:87` and `edit.go:212 syncRecipes` → `recipes.List(v.OS, backendOf(v))`
and `recipes.List(v.OS, backendForMode(v, e.mode))`.
`core.Recipes(RecipeFilter{OS, Backend})` (`apply.go:178`) → the same call,
plus `Label`, `TargetOS`, `Shared`.

No behavioural disagreement today: all four reach `recipes.List`. Two things
to note:

- `core.recipeLabel` (`apply.go:196`) is an acknowledged hand copy of
  `tui/labels.go:22 recipeLabel`. Its own comment says so.
- The TUI never shows *why* a recipe is not offered. `core.CheckRecipes`
  (`apply.go:256`) returns reasons, and `recipes.UnsupportedReason`
  (`metadata.go:163`) exists and is wired into neither (open task #12). The
  enforcement-with-an-explanation that §5 of the design doc calls "the whole
  point" is currently unreachable from any user-facing surface.
- Because of D4, the edit screen can ask `recipes.List` for a different
  (OS, backend) pair than the create form did for the same VM.

### D12. The ssh argv. `core.SSHCommand` is not usable for the TUI's `s` key.

`tui/ssh.go:63 sshIntoArgs` builds its own argv and deliberately does **not**
set `BatchMode=yes` (`ssh.go:30-34`), because a human is at the keyboard and
a disk VM with no key installed may only be reachable by password.

`core.SSHCommand` (`access.go:32`) returns `append([]string{"ssh"}, sshx.Args(v)...)`,
and `sshx.Args` sets `BatchMode=yes` (per `core/exec.go:34`). So
`core.SSHCommand` returns the **unattended** argv, not "the argv a human
would run" as `core-api.md` §7.1 item 1 describes it. Pointing the TUI's `s`
key at it would turn a password prompt into "Permission denied".

Related doc drift: §7.1 item 1 and guest-subsystem §4 item 13 both describe
`tui/ssh.go:49` as hardcoding `root@`. It does not any more: `ssh.go:77`
uses `sshx.User(v)`, and `unreachableMsg` names the real installer. Those two
doc entries are stale.

### D13. cloud-init readiness: the TUI is the only implementation, and the doc says it is wrong.

`tui/cloudinit.go:46 checkCloudInit` shells out `ssh … cloud-init status` and
parses the text `status:` line. guest-subsystem §7 says to replace that with
`cloud-init status --format json` and use the per-stage records. Nothing in
core does this; `core.Wait` has no `UntilCloudInit`, and `core.VM` has no
`Progress` (core's `vm.go:30-36` says so explicitly, and gives a good reason).

So this is a gap, not a divergence: core owes the TUI a call it does not
have, and the TUI's `cloudInitFraction` (`cloudinit.go:76`) invents a
0.15/0.6/1.0 stage fraction because there is no real stage list to render.

### D14. Delete refusal. Agree for good VMs, diverge for broken ones.

`list.go:193` refuses a running VM with a toast. `core.Destroy` refuses with
`ErrAlreadyRunning`. Same rule. The broken branch is D5.

### D15. Identity: directory vs the `name` field.

`core.vm.go:129-143` is emphatic that a VM's identity is its **directory**,
and `VM.Name` reports `filepath.Base(v.Dir)`. It cites a fixed bug where
`List()[0].Name` returned the vm.toml `name` and `Get` on it failed.

The TUI keys everything on `v.Name`, the vm.toml field: `vmByName`
(`provstep.go:159`), the `provisioning` map, `cloudInit` map, toasts,
`brokenPortHolder(port, self)`'s `self`. `deleteVM` is the exception and is
correct (it goes through `v.Delete()`, which uses `Dir`, pinned by
`TestDeleteTargetsDirectoryNotName`).

The `E` key lets a user set `name = "other"` in vm.toml. After that the TUI's
maps key on `"other"` while `core.Get("other")` returns `ErrNotFound`.
Migrating means changing every lookup key from `v.Name` to the directory
base. Mechanical, but it touches app.go, list.go, provstep.go, detail.go and
edit.go.

---

## 3. What must not move

Flagged because some of it *looks* like orchestration.

**Unambiguously presentation, leave alone:** `theme.go`, `fields.go`,
`toast.go`, `keymap.go` in full, `labels.go` (`modeLabel`, `modeHint`,
`wrapItems`; `recipeLabel` is duplicated in core but the TUI's copy is the
original and is fine), `vmlist.go`'s delegate and every width constant,
`app.go`'s `View`/`newView`/`renderModal` compositing, `list.go brokenReason`,
`access.go`'s `shortenPath`/`joinAccess`.

**Every width constant has a story:** `listWidth = 60`
(sized to a running row), `formContentWidth = 72` (sized to fit 80 columns),
`byoFileWidth = 24`, `imageMetaWidth = 11` (sized to `"13 (trixie)"`),
`modalSizeWidth = 9` (sized to `"~66.0 MiB"`), `accessWidth = 40`,
`modalHeightChrome = 11`. Each carries a comment naming the exact value that
broke it. None of these should be touched by a migration.

**Looks like orchestration, is genuinely presentation:**

- `imagemodal.go` and `imagescan.go` in full. The two-level picker, the byo
  fuzzy finder, `scanImages`, `foundRow`'s column negotiation, `middleElide`,
  `dirTail`. `scanImages` walks the host filesystem for a *picker*; core has
  no business owning it. The one part that duplicates core is
  `byoOptionFromPath` (§1.1), and even that must keep returning an
  `imageOption`, because `mo.images` is indexed by the modal.
- `download.go` in full: `dlProgress`, `dlStats`, `ratio/speed/eta/line`,
  `humanBytes`, `humanDuration`, `bar`, `dlView`. A progress *display*. The
  only line that moves is `fetchImage`'s `iso.Download` call.
- `form.go`'s focus machinery: `order()`, `focusOrder.next/prev`, `refocus()`,
  `selectImage()`, `recipesLabel()`. **`order()` reads `resolvedBackend()` and
  `effectiveMode()`**, so those two must survive as view-model derivations
  even once core owns the authoritative copy; the form decides which rows to
  draw before a `Spec` exists. They are duplicated derivation and deleting
  them breaks the form. The right shape is to keep them and add a test
  asserting they agree with what `core.Plan` produces for the same Spec.
- `provstep.go` in full: `readProvStep`, `provLine`, `provLines`,
  `provElapsed`, `tailBytes`, `newSpinner`, `provMaxLast`. `core.Apply`'s doc
  comment (`apply.go:56-61`) names provstep.go as the intended reader of the
  apply log and says so is why Apply invents no second progress path. The
  deliberate absence of a percentage here (`provstep.go:20-23`) is the same
  rule `core-api.md` §1 generalises for `Progress.Stages`.
  **One exception:** `recipeMarker = "=== recipe "` (`provstep.go:60`) is a
  wire format shared with `sshx.Provision`'s writer across a package
  boundary, with nothing enforcing it. That is a contract, not a rendering
  choice. It wants one exported home, or at minimum a test on both sides.
- `autoprov.go`'s **`wantsAutoProvisionPrompt`** (`:47`). This is a real
  decision and it is the file's reason to exist, but it is a *UI* policy
  ("should I interrupt the user with a y/N"), not an orchestration one. It
  encodes that a live VM's tmpfs root means a previous run is gone, that an
  uninstalled disk VM's sshd belongs to the installer, and that a cloud VM
  has nothing to do. core deliberately has no equivalent and should not get
  one: `core-api.md` §6 says consent is the caller's to supply. **Do not move
  this.** Move the mechanism underneath it (`awaitSSH` → `core.Wait`), keep
  the decision.
  Same for `autoProvisionPrompt` (`:89`), which is a sentence.
- `access.go accessBox`'s per-mode "how do I get in" branch. It looks like OS
  knowledge; it is a rendering of it. Fine to keep, but after Phase 1 it
  should read from a `core.VM` rather than re-branch on `v.Mode`.

**Looks like presentation, is actually orchestration, and should move:**

- `autoprov.go:101 ensureNoStaleLog`. It deletes a file on the host as a side
  effect of pressing enter on a live VM, so that `lastProvisionSucceeded` and
  the detail tail do not describe a run whose effects the reboot wiped. That
  is a lifecycle invariant, and it is invisible to `stoat up` today: the CLI
  has no equivalent, so a live VM started from the CLI keeps a stale apply
  log. It belongs in `core.Start`.

---

## 4. Missing surfaces

Keys already taken:

- **list**: `q ? j k ↑ ↓ / pgup pgdown home end g G esc enter r n → l s p d`,
  plus `y` while a prompt is armed, plus `ctrl+c` centrally.
- **detail**: `esc ← h q ? e E i s p t c`.
- **form / edit**: `esc ? tab shift+tab ↑ ↓ ← → space enter`.

Free on list: `a b c e f i m o t u v w x z` and most capitals.
Free on detail: `a b d f g j k l m n o r u v w x y z` and all capitals but `E`.

| Surface | Where it belongs | New screen? | Effort | Notes |
|---|---|---|---|---|
| **Doctor** | the existing `m.preflight` block (`app.go:122`) | no | trivial | Already runs `qemu.Preflight()` and shows one string. `core.Doctor()` returns structured checks **with `Fix` commands** that the TUI would currently throw away. Cheapest item on this list by a wide margin. |
| **Prune** | list key `x` | no, reuses the `pendingDelete` y/N prompt | low | `core.Prune(PruneOpts{DryRun:true})` → show the candidate list → y/N → re-run with `DryRun:false`. Maps exactly onto the existing prompt shape. Caveat: `Prune` returns pre-formatted `[]string` (open task #10); the TUI would be rendering strings core formatted. Do #10 first. |
| **Logs (console)** | detail key `L`, **and** a path from a broken list row | yes, a small pager (bubbles/viewport) | low-medium | The console log is unreachable from the TUI today; `detail.go` tails only the apply log. `core.Logs`' doc says a broken VM is exactly the VM whose console output someone needs, and broken rows currently offer nothing but delete. Static tail is low effort; a scrolling viewport is medium. |
| **Forward** | detail read-only row first, then an `eForwards` row in edit.go | no | medium (read) / high (edit) | Forwards are invisible everywhere in the TUI, which is D1's root cause. Showing them is cheap and closes half the bug. Editing them means a variable-length row in edit.go's index-based row model, which is new. `Forward` returns `active bool`: the UI must render "in effect now" distinctly from "applies at next start", per `core-api.md` §8 decision 5's rule, and getting it wrong is the specific failure that decision exists to prevent. |
| **Clone** | list `c`, detail `C` | a one-field name prompt | medium | `core.Clone(name, newName)` is one call. The work is the free-text name prompt (`m.status` only handles y/N, so this needs a textinput; smallest version reuses `theme.TextInput()` the way `openByo` does) and the `ErrAlreadyRunning` "stop it before cloning" path. |
| **Snapshots** | detail `S` | **yes**, a modal | high | It is a list with per-row actions, not a key: `enter` restore (with a y/N, since Restore is unambiguously destructive), `d` delete, `n` new (needs a tag input). The skeleton is copyable from `imagemodal.go`'s byo screen (list + textinput + pane + `resize`). A live VM must render `ErrNoDisk`'s "diskless by design" rather than an empty list. Highest value of the lot per `core-api.md` §7.1. |
| **Update** | not a new surface: it is edit.go's save path | no | see Phase 4 | |
| **Wait** | not a user-facing surface, it replaces `awaitSSH`'s internals | no | n/a | |
| **Exec** | **recommend not exposing** | n/a | n/a | The TUI already has `s`, which is strictly better for a human at a keyboard. `Exec` exists for the MCP server (`core-api.md` §5: "the single most valuable operation for an agent"). Adding a command palette to the TUI to reach it would be inventing a surface for a caller that is not there. |
| **CopyTo / CopyFrom** | detail `u` / `U`, if wanted | two path prompts | medium | Weak fit: a human with a share or an ssh session already has both. Recommend deferring until something asks for it. |
| **Images / DownloadImage** | not new, replaces `buildImages` / `fetchImage` | no | low | Comes with a real behaviour gain: `esc` during a download actually cancels (`app.go:328`'s named open item). |

---

## 5. Sequenced plan

Each phase ships on its own and leaves the suite green.

### Phase 0: unbreak the build (blocking, one owner)

Fix `autoprov.go:30` and `ssh.go:47` for the new `sshx.Wait` signature.
Nothing below is verifiable until this lands. See §0.

### Phase 1: read paths onto core. Behaviour-preserving.

**1a. `loadVMs` → `core.List()`. The spine, and the bottleneck.**

The model would hold `[]core.VM` instead of `[]*config.VM` + `[]config.Broken`.
This reshapes `m.vms`, `m.broken`, `vmItem`, `current()`, `currentBroken()`,
`vmByName`, and every `qemu.Running(v)` call site.

**Blocker found:** `core.VM` cannot replace `config.VM` in the TUI as it
stands. `detail.go` renders `v.ISO` (`:205`), `v.Base` (`:207`) and
`v.ConsolePassword` (`:243`); `access.go` renders `v.ConsolePassword`
(`:60`). `core.VM` has none of those three. Two ways out:

1. Add `ISO`, `Base` and `ConsolePassword` to `core.VM`. `ISO` and `Base` are
   plain vm.toml facts with no reason to be withheld, and an MCP `vm_status`
   would want them. `ConsolePassword` is a **judgement call**: putting a
   password on the API's public view type is a decision about the API's
   security posture, not a mechanical fix, and it should be made
   deliberately (a `HasConsolePassword bool` plus a separate accessor is the
   obvious alternative).
2. Keep `config.Load` for the detail and access panes only. Cheaper, but it
   leaves two views of a VM live in one program, which is the thing this
   migration is trying to end.

Recommend (1), with the ConsolePassword half raised as its own question.

Owner: **app.go, vmlist.go, list.go, provstep.go as one unit.** Nothing else
may touch those files during 1a.

**1b. `core.Doctor()` into the preflight block.** One file (app.go), so it
**collides with 1a** and must be sequenced after it, or folded into it.

**1c. `buildImages()` → `core.Images()`**, keeping `imageOption` as the view
type built *from* a `CatalogImage`. Owner: **form.go**. Fully parallel with
1a.

### Phase 2: lifecycle. Behaviour-preserving except one fix.

`startVM`/`stopVM` → `core.Start`/`core.Stop`; `deleteVM`/`deleteBrokenVM` →
`core.Destroy` (fixes D5). Move `ensureNoStaleLog` into `core.Start` (§3), or
keep it and record why not.

Owner: **list.go** (plus a small core change for `ensureNoStaleLog`).
Parallel with Phases 3 and 4 once 1a's model type is settled.

### Phase 3: provisioning and readiness.

- `provision()` → `core.Apply(ctx, name, core.ApplyOpts{})`.
- Keep every `startProvision` refusal **in front of** the call: D9b (not
  installed) and D9c (zero recipes) have no core equivalent and would
  regress. Re-key D9a's cloud refusal on backend rather than mode.
- `awaitSSH` → `core.Wait(ctx, name, core.UntilReachable)` with a 90s ctx,
  preserving silence on failure (D7).
- `lastProvisionSucceeded` (D8): **judgement call.** Do not delete it in
  favour of a `Wait(UntilApplied)` call; that is a blocking call being used
  as a predicate, which is the wrong shape. Either core exports the predicate
  (`core.LastApplyOK(name) (bool, error)`) or the TUI keeps its copy and both
  get one shared test.

Owner: **provision.go, autoprov.go**. Parallel with Phase 2.

### Phase 4: edit onto `core.Update`. The only non-mechanical phase.

**4a. The behaviour-preserving half.** RAM, CPUs, Share, Recipes, SSHPort,
Disk → build a `core.Patch`, call `core.Update`. This fixes D1 and D2 and
buys the data-root lock. It **changes the wording of every validation error**
the pane shows.

**4b. The mode switch (D3).** Three options; pick one deliberately:

1. Drop it from the TUI, note it in the release notes. Shippable today.
2. Give `core.Patch` a `Mode` that is allowed when the VM has never booted.
   Needs the mode-agnostic "has started" signal `update.go:88-104` says does
   not exist: a new persisted field on `config.VM`. This is the right
   answer.
3. Keep edit.go's mode switch outside `core.Update` as a TUI-only escape
   hatch. **Do not do this.** It re-opens the exact divergence the migration
   exists to close, and it is what makes D9a reachable.

Recommend (2) as correct and (1) as shippable; decide before starting 4a, not
after.

**4c. `detail.go`'s `i` key.** Needs `Patch.Installed` or it stays a direct
unlocked `Save()`. Small, but it is a second writer to vm.toml.

Owner: **edit.go, detail.go**. Single-file bottleneck on edit.go; nothing
else may touch it.

### Phase 5: new surfaces. Fully parallel, one file each.

New files, one owner each: `prune.go`, `snapshots.go`, `clone.go`,
`logview.go`. Forwards go into detail.go (read-only) then edit.go (editable),
so they queue behind Phase 4.

**Collision management:** `list.go` and `detail.go` key switches are each
touched by several of these. Assign **one owner per key-registration file**:
one agent owns list.go's switch, one owns detail.go's, with the modal
bodies in the new files owned by whoever writes them. `keymap.go` is a shared
single-file bottleneck: every new binding adds a `key.Binding` there.
Recommend batching keymap.go edits under one owner at the end of the phase.

### Parallelism summary

- **Serial spine:** Phase 0 → 1a (+1b) → everything else.
- **Genuinely parallel once 1a's model type is settled:** 1c, 2, 3, and each
  Phase 5 new file.
- **Single-file bottlenecks:** `app.go` (model type and message routing),
  `edit.go` (all of Phase 4), `keymap.go` (every new binding), and the
  `list.go` / `detail.go` key switches.
- **Judgement calls, not mechanical work:** D3's mode switch; `core.VM`'s
  missing `ISO`/`Base`/`ConsolePassword`; whether `ensureNoStaleLog` moves
  into `core.Start`; whether `lastProvisionSucceeded` is shared or exported;
  whether `core.SSHCommand` is fixed for interactive use or the TUI keeps
  `sshIntoArgs` (D12).

---

## 6. Risks

### Tests likely to break

The TUI has 138 tests across 19 files. The width- and layout-sensitive ones:

- `geometry_test.go`: `TestListPaneFitsTheTerminal`,
  `TestListWidthFitsARunningRow`, `TestFooterNeverOverflows`.
- `form_test.go`: `TestFormPaneWidthIsStable`, `TestFormTabOrder`,
  `TestImageMetaColumnFitsEveryValue`.
- `imagemodal_test.go`: 47 tests, many pinning exact column widths
  (`modalSizeWidth`, `modalVariantWidth`, `byoFileWidth`). **Phase 1c is the
  main risk here**: `CatalogImage` carries the same fields, so the row must
  come out byte-identical.
- `vmlist_test.go`: `TestSelectedRowIsFullyHighlighted`, sensitive to
  anything that changes where a styled segment starts or ends.

Rule-pinning tests that will need rewriting, and are therefore signals to
stop and check (guest-subsystem §10: "any test that needs updating is a
signal to stop and check rather than to edit"):

- Phase 4a: `TestEditRefusesDiskShrink`, `TestEditRefusesPortCollision`,
  `TestEditValidatesRAMAndCPUs`, `TestEditCloudVMSavesWithoutADiskSize`,
  `TestEditDoesNotMutateUntilSaved`.
- Phase 4b (if the mode switch goes): `TestModeSwitchToDiskNeedsADiskFile`,
  `TestModeSwitchOffCloudNeedsAnISO`, `TestModeSwitchResyncsRecipes`,
  `TestEditTabOrderSkipsDiskInLiveMode`.
- Phase 3: `TestWantsAutoProvisionPrompt`, `TestLastProvisionSucceeded`,
  `TestAcceptedOfferProvisions`, `TestOfferNeverPreemptsADeletePrompt`.
- Phase 1a: `TestDeleteTargetsDirectoryNotName` (pins D15's rule; it should
  keep passing, and if it does not, that is a real regression).
- Phase 1c: `TestMatchLocalImageFindsDownloadedAlpineCloudFile`,
  `TestBrowsedByoCloudinitResolvesSSHUser` (the latter is the only caller of
  the dead `resolvedSSHUser`, §1.2; deleting the function deletes the test).

### Behaviour-preserving vs behaviour-changing

**Behaviour-preserving:** Phase 1a (given the `core.VM` field fix), 1b, 1c,
Phase 2's start/stop, Phase 3's `awaitSSH` swap, Phase 4a's happy path.

**Behaviour-changing, user-visible, must be called out in release notes:**

- **Phase 2:** deleting a broken VM now refuses while a qemu process is still
  running against its directory. Correct (D5), but a keystroke that used to
  "work" starts failing.
- **Phase 3:** the cloud refusal keys on backend, so a mode-switched VM that
  used to appear to provision now refuses. Correct (D9a).
- **Phase 4a:** every validation message on the edit screen changes wording,
  and two previously-accepted edits are now refused (a port colliding with
  another VM's forward; a resize on a VM whose current size will not parse).
- **Phase 4b option 1:** the mode switch disappears from the edit screen.
- **Phase 1c / `core.DownloadImage`:** `esc` during a download now cancels it
  rather than orphaning the goroutine. Better, but it is a change in what the
  key does, and it needs new model state (a stored `cancel` func).
- **Phase 5:** new footer bindings. `renderFooter` (`keymap.go:58`)
  `ansi.Truncate`s the short help to the terminal width, and
  `TestFooterNeverOverflows` checks it does not *wrap*, not that everything is
  *visible*. Adding four bindings to `listHelp.ShortHelp` will silently push
  `q` and `?` off the right edge on an 80-column terminal. Plan new keys as
  `FullHelp`-only, or reorder deliberately.

### Other risks

- **Two writers to vm.toml outside the lock** survive until Phase 4c:
  `detail.go`'s `i` key and its `E` key. `core.Create`/`Update`/`Destroy`/
  `Clone`/`Prune` all take `config.Lock()`; these do not.
- **`recipeMarker` is an unenforced cross-package contract** (§3). Nothing
  fails if `sshx.Provision`'s marker and `provstep.go`'s parser drift, and
  `core.Apply`'s doc comment now depends on them agreeing.
- **The `Share` write-back asymmetry** (D10d) will start rewriting `~/...`
  to absolute paths on every edit once Phase 4 lands. Decide whether that is
  wanted before it happens silently.
- **Doc drift to fix while in the area:** guest-subsystem §4 item 13 and
  core-api §7.1 item 1 both describe `tui/ssh.go` as hardcoding `root@`. It
  no longer does.
