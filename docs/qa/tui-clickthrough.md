# TUI click-through: manual smoke test

The checks an agent cannot run, because they need a real Alpine boot and a human
reading the screen. Run these after any change to the list or detail screens.

Data root is `~/.stoat` (or `$STOAT_HOME`). Each VM is a directory
`~/.stoat/<dir>/` that holds a `vm.toml`. Build and launch the TUI with `just run`
or `go run ./cmd/stoat`.

Keys, for reference. List screen: `enter` start/stop, `l`/`→` details, `s` ssh,
`p` provision, `d` delete, `n` new, `r` edit recipes, `/` search. Detail screen:
`e` edit form, `E` raw vm.toml in `$EDITOR`, `i` installed toggle, `s` ssh, `p`
provision, `L` console log, `S` snapshots, `esc`/`h`/`q` back.

## 1. VM start: row flip and uptime tick

Verifies the running dot and the `time.Since(StartedAt)` uptime that ticks with
no list refresh.

1. On the list, select a stopped VM. The dot is the stopped glyph, the row shows
   `-`.
2. Press `enter`. The VM starts.
3. Watch the row. The dot flips to the running glyph. The row reads
   `up 1s :PORT`, then `up 2s`, then `up 3s`. The count climbs every second on
   its own.
4. Confirm the seconds keep climbing without any keypress. A frozen number means
   the uptime regressed to a stored duration.
5. Press `enter` again to stop it. The dot returns to stopped and the uptime
   drops to `-`.

## 2. Corrupt vm.toml: shows broken, deletes with d+y

Verifies a broken VM still lists and deletes, keyed by its directory.

1. Stop the TUI. Pick a directory name that sorts mid-list, e.g. `mmm-broken`.
   Create `~/.stoat/mmm-broken/vm.toml` with garbage: `not = valid = toml`.
2. Launch the TUI. `mmm-broken` appears in alphabetical position, marked broken.
3. Select it and press `l`. The detail screen still opens; `L` serves any
   console log the directory holds.
4. Back on the list, press `d`. A `y/N` confirmation appears.
5. Press `y`. The row is removed and `~/.stoat/mmm-broken/` is gone.
6. Repeat step 4. Press any key other than `y`. The TUI cancels the delete and
   the row stays.

## 3. vm.toml name differs from directory

Verifies every operation keys off the directory, never the `name` field. This
identity bug recurred before. It must stay fixed.

1. Stop the TUI. In a working VM at `~/.stoat/realdir/vm.toml`, change the `name`
   field to `wrongname` (leave the directory `realdir`).
2. Launch the TUI. The row shows `realdir`, the directory, not `wrongname`.
3. Press `l` for details. Run each key below. Each one must act on this VM, and
   none may report "not found":
   - `E` opens `realdir/vm.toml` in `$EDITOR`.
   - `L` shows this VM's console log.
   - `s` opens an ssh session to it (VM must be running).
   - `p` starts a provision run against it.
4. Restore the `name` field afterward if you care about it.

## 4. Snapshots modal (S), four states

Verifies the `S` modal and both confirmation-gated destructive paths. Snapshots
need a disk or cloud VM, not live.

1. On the detail screen of a VM with no snapshots, press `S`. The modal
   opens and shows an empty-state line, not a blank box.
2. Press `d`, then `r`. Neither does anything on an empty list. `esc` closes the
   modal.
3. Take a snapshot first (`stoat snapshot <vm> <tag>` from a shell), then reopen
   the detail screen and press `S`. The snapshot lists by its tag and date.
4. Press `d`. A `delete clean? y/N` line appears. Press a non-`y` key. The TUI
   deletes nothing. Press `d` then `y`. The TUI removes the snapshot and
   refreshes the list.
5. Take another snapshot. Press `r`. A `restore clean? y/N` line appears. Confirm
   with `y` and the VM's disk rolls back to that snapshot.
6. Run steps 1-5 once with the VM stopped and once running. Both must behave the
   same. A running VM's `info snapshots` prints `--` for the ID. The modal keys
   everything off the tag for that reason.
