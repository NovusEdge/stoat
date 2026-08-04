# Spec: authoring recipes in stoat

Status: **proposal, not built.** Written 2026-07-31 in response to "a selector
or some kinda recipe creator if that's something we can spec out".

## What a recipe is today

A file in `~/.stoat/recipes/`, named `<name>.<os>.sh` or `<name>.cloud.yaml`.
Shell recipes are piped into `sh -s` over ssh; cloud fragments are merged into
the cloud-init seed. `recipes.List(os, backend)` filters by the suffix, so the
picker only ever offers files that can run on the selected image.

There are 7 files and 441 lines today. `Install()` copies the bundled ones into
the data root and **never overwrites**, so editing one in place already works
and survives upgrades.

## The thing worth noticing first

**Authoring a recipe is already supported.** Drop a file in `~/.stoat/recipes/`
named `mything.alpine.sh` and it appears in the picker for Alpine VMs. No code
required. Any "creator" competes with `$EDITOR` on a path that already works.

So the question is not "can we add a creator" but "what does a creator do that
`vim ~/.stoat/recipes/x.alpine.sh` doesn't". Three honest candidates below,
smallest first.

---

## Option A: `stoat recipe new <name>` (scaffold only)

Writes a skeleton with the parts every recipe needs and gets wrong:

```sh
#!/bin/sh
set -e
setup-apkrepos -c -1        # community repo + mirror + index refresh
apk add <packages>
root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)
case "$root_fstype" in
tmpfs | overlay) echo "NOTE: live VM, this is gone on reboot." ;;
*)               echo "installed on a disk VM, survives a reboot." ;;
esac
```

then opens `$EDITOR` on it.

- **Solves:** the live-vs-disk honesty block (which the test suite *requires* of
  every shell recipe and which is easy to forget), the repo-enable line, `set -e`.
- **Cost:** ~40 lines, one CLI subcommand, one embedded template per backend.
- **Doesn't solve:** per-OS variants, package-name differences.

## Option B: a manifest that generates the per-OS files

```toml
# ~/.stoat/recipes/src/docker.toml
name = "docker"
[packages]
alpine = ["docker", "docker-cli-compose"]
debian = ["docker.io", "docker-compose-v2"]
arch   = ["docker", "docker-compose"]
[post]
alpine = "rc-update add docker default && rc-service docker start"
debian = "systemctl enable --now docker"
```

`stoat recipe build` expands it into `docker.alpine.sh`, `docker.debian.sh`, …
with the boilerplate filled in.

- **Solves:** one place per recipe instead of N files; the OS-specific parts are
  adjacent so a missing variant is obvious.
- **Cost:** ~200 lines, a template engine, a second file format to learn, and a
  generated/source distinction (do you edit the .sh? what happens to your edit?).
- **Doesn't solve:** the actually hard part: knowing that Alpine calls it
  `docker-cli-compose`, Debian `docker-compose-v2`, and Fedora needs
  `@xfce-desktop-environment` rather than `xfce4`. A manifest doesn't discover
  package names; a human still verifies each one.

**This is the trap.** The per-OS files look like duplication, but the duplicated
part is boilerplate (~15 lines) while the varying part is exactly the knowledge
a generator can't supply. Compressing 3 files into 1 manifest saves ~45 lines
and costs ~200.

## Option C: an in-TUI editor

A pane that lists recipes, with new/edit/delete and a text area.

- **Cost:** `bubbles/textarea` pulls in `clipboard`, `go-udiff` and `heredoc`
  (three new modules) to reimplement, badly, an editor the user already has
  configured. The detail screen's `E` already shells out to `$EDITOR` for
  `vm.toml`; the same trick works for recipes at a fraction of the cost.
- **Recommendation: no.**

---

## Recommendation

**Option A, plus two things worth more than any of them:**

1. **`r` on the list/detail screen opens the recipes directory in `$EDITOR`**,
   the `E` pattern, ~10 lines, and it makes the existing authoring path
   discoverable, which is its real problem.
2. **A `stoat recipe check <file>`** that runs `sh -n` and asserts the
   live-vs-disk block is present. That is the one class of mistake the test
   suite catches for bundled recipes and nothing catches for the user's own.

Option B only becomes worth it past roughly a dozen recipes across four OSes.
At 7 files it would be more machinery than the thing it manages.

## What this does NOT propose

- A DSL. Phase 4 already rejected one: "per-OS templates = per-OS files, no DSL".
- Fetching recipes from a registry. That is a supply-chain question, not an
  authoring one, and it needs signing before it needs a UI.
- Storing secrets so a recipe can authenticate (e.g. a tailscale auth key). Same
  reasoning as `tailscale.alpine.sh`'s header: plaintext in the data root or in
  git are both worse than telling the user to run one command.

## Open question for the user

Which of these is the actual pain?

- "I don't know how to write one" → Option A + discoverability.
- "I have to write it three times for three OSes" → Option B, and it is a real
  cost, but see the trap above.
- "I want to edit them without leaving the TUI" → the `E`-style `$EDITOR` hop,
  not a built-in editor.
