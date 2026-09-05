# Contributing to stoat

## Setup

```sh
just setup      # builds, installs to ~/.local/bin, reports missing host deps
just dev        # runs the TUI against a scratch STOAT_HOME
```

`just setup` also installs the git hooks. A repository with a `stoat.toml`
declares its own VMs; run `stoat up` in it to build them.

Go 1.26 (pinned in `go.mod`), `just`, and for anything that boots a VM:
KVM, `qemu-system-x86_64`, `qemu-img`, `ssh`. `stoat doctor` lists what is
missing. `just lint` also needs `golangci-lint` v2 and `shellcheck`.

## Branches and pull requests

- Branch off `main`. One change per branch.
- Every PR is squash merged. The PR title becomes the commit subject, so
  write it in the commit grammar below.
- Sign off every commit (`git commit -s`). The DCO check on the PR reads
  the `Signed-off-by` trailer and blocks a merge without it.
- No `Co-Authored-By` or tool-attribution trailers. The `commit-msg` hook
  strips them.
- A PR that changes a bundled recipe, a guest, or the boot path gets the
  `needs-live-boot` label and carries the pasted output of the boot it was
  tested with.

## Commit grammar

`type(scope): imperative subject`, under 60 characters.

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`. Scope is the
package or area: `recipes`, `tui`, `cloud`, `sshx`, `mcp`.

The body says what changed and why. It does not narrate how the answer
was found.

## Gates

`just check` runs gofmt, `go vet`, and `go build`. The pre-commit hook
runs the same. `just lint` runs golangci-lint and shellcheck. CI runs
all of those plus `go test ./...` on every PR. The DCO app checks the
sign-off trailer on every commit in a PR.

## Tests

| tier | command | needs |
|---|---|---|
| unit | `just test` | nothing |
| race | `just race` | nothing |
| TUI model tests | `just test-pkg internal/tui` | nothing |
| e2e | `just e2e` | KVM, network, ~15 minutes |

A change to `internal/core`, `internal/sshx`, `internal/cloudinit`,
`internal/apkovl`, or a bundled recipe needs an e2e run or a live boot on
the affected guest, with the output pasted in the PR.

Test helpers live in `internal/testutil`. `FakeVM` is the fixture every
CLI and core test uses.

## Docs

Docs live under `docs/` in GitBook layout; `docs/SUMMARY.md` is the table
of contents. Write in Simplified Technical English: one fact per
sentence, active voice, name the actor, simple tenses. No filler
vocabulary, no contrast constructions, no section headers for one
paragraph.

A comment in code carries a fact the reader cannot get from the code.
Paraphrase, investigation history, and section banners are deleted in
review.

## Design changes

A change to a subsystem or an interface another package depends on
starts as an issue that states the design, discussed before the
implementation PR. Settled decisions go in `docs/design/`.

`docs/design/core-api.md` §12 holds the conventions a new CLI command
follows: the `wire` struct behind `--json`, `fail` against `failMsg`, the
shared `confirm` helper, kong aliases, and `tomlx` for a TOML file. A
reviewer rejects a PR that ignores them.

## Recipes

Bundled recipes are `xfce`, `docker`, `devtools`, `tailscale`, and the set
is closed. Write a new recipe in `~/.stoat/recipes/<name>/` from `stoat
recipe new`; see `docs/recipes/writing-your-own.md` and
`docs/recipes/sharing.md` for installing and pinning remote recipes. Every
recipe script starts with `set -e` and carries the live-vs-disk block that
`stoat recipe new` scaffolds; `internal/recipes/recipes_test.go` checks both
for the bundled set.

## Reporting a security issue

Open a private security advisory on the GitHub repository. Do not open a
public issue for it.
