# TUI click-through: manual smoke test

Run these checks with real guests after changes to the list or detail screens.
Inspect the rendered screen as well as the command output.

Use a separate data root for these checks. They modify VM configuration,
delete test VMs, and restore snapshots. Set `STOAT_HOME` to a scratch directory
before creating the guests. Build and launch the TUI with `just run` or
`go run ./cmd/stoat`. Paths below use `$STOAT_HOME`.

Keys, for reference. List screen: `enter` start/stop, `l`/`→` details, `s` ssh,
`p` provision, `d` delete, `n` new, `r` edit recipes, `/` search. Detail screen:
`e` edit form, `E` raw vm.toml in `$EDITOR`, `i` installed toggle, `s` ssh, `p`
provision, `L` console log, `S` snapshots, `esc`/`h`/`q` back.

## 1. VM start and uptime display

Verify the running indicator and elapsed uptime. The current list has no
periodic refresh: uptime changes when input or another event causes a render.
The idle refresh check below records that limitation.

1. On the list, select a stopped VM. The dot is the stopped glyph, the row shows
   `-`.
2. Press `enter`. The VM starts.
3. Watch the row. The dot changes to the running glyph and shows `up ... :PORT`.
4. Wait a few seconds without input and record whether uptime changes. Press
   `j` or `k` and confirm that the next render shows the elapsed time. Return
   the selection to the VM from step 1 and confirm its name before continuing.
5. Press `enter` again to stop that VM. The dot returns to stopped and the
   uptime drops to `-`.

## 2. Corrupt vm.toml: shows broken, deletes with d+y

Verifies a broken VM still lists and deletes, keyed by its directory.

1. Stop the TUI. Pick a directory name that sorts mid-list, e.g. `mmm-broken`.
   Create `$STOAT_HOME/mmm-broken/vm.toml` with garbage: `not = valid = toml`.
2. Launch the TUI. `mmm-broken` appears in alphabetical position, marked broken.
3. Select it and press `l`. The TUI stays on the list and reports that
   `vm.toml` is broken.
4. Press `d`, then `n`. The confirmation closes and the row remains.
5. Press `d`, then `y`. The row and `$STOAT_HOME/mmm-broken/` are removed.

## 3. vm.toml name differs from directory

Verifies every operation keys off the directory, never the `name` field. This
identity bug recurred before. It must stay fixed.

1. Stop the TUI. In a test VM at `$STOAT_HOME/realdir/vm.toml`, change the `name`
   field to `wrongname` (leave the directory `realdir`).
2. Launch the TUI. The row shows `realdir`, the directory, not `wrongname`.
3. Press `l` for details. Run each key below. Each one must act on this VM, and
   none may report "not found":
   - `E` opens `realdir/vm.toml` in `$EDITOR`.
   - `L` shows this VM's console log.
   - `s` opens an ssh session to it (VM must be running).
   - `p` starts a provision run against it.
4. Restore the original `name` field after the check.

## 4. Snapshots modal (S), four states

Verifies the `S` modal and both confirmation-gated destructive paths. Snapshots
need a disk or cloud VM, not live.

1. On the detail screen of a VM with no snapshots, press `S`. The modal
   opens and shows an empty-state line, not a blank box.
2. Press `d`, then `r`. Neither does anything on an empty list. `esc` closes the
   modal.
3. Take a snapshot named `clean` first (`stoat snapshot <vm> clean` from a
   shell), then reopen the detail screen and press `S`. The snapshot lists by
   its tag and date.
4. Press `d`. A `delete clean? y/N` line appears. Press a non-`y` key. The TUI
   deletes nothing. Press `d` then `y`. The TUI removes the snapshot and
   refreshes the list.
5. Take another snapshot named `clean`. Press `r`. A `restore clean? y/N` line
   appears. Confirm with `y` and verify that the VM returns to that snapshot.
6. Run steps 1-5 once with the VM stopped and once running. The modal and
   confirmation behavior must match. Snapshot semantics differ: a stopped
   snapshot stores disk state, while a running snapshot also stores VM memory
   and restores in place. A running VM's `info snapshots` prints `--` for the
   ID. The modal keys everything off the tag for that reason.
