# Development process

Cross-cutting. Applies to every implementation plan from the five feature
specs. Status: approved 2026-09-04.

## Goal

An outside contributor can build, test, and land a change or a recipe
with the same gate a maintainer uses, and the gate runs in CI.

## Today

CI runs gofmt, vet, build, test, and the Python MCP tests. The pre-commit
hook mirrors the Go gate and needs `just hooks` to install. There is no
shell lint, no Go linter, no e2e in CI, no dependabot, no PR or issue
template, no CODEOWNERS, no CONTRIBUTING, SECURITY, or CODE_OF_CONDUCT.
Bundled recipes are checked statically for `set -e` and the live-vs-disk
block; only `xfce` is boot-tested, by hand.

## Contribution guide

`CONTRIBUTING.md` at the root, written with current facts only. Sections:
setup, branch and PR flow, commit grammar, gates, test tiers, docs style,
specs, recipes. It links to `docs/design/core-api.md` for code conventions
and to this spec for what is planned.

## Recipe path

- The bundled set stays at `xfce`, `docker`, `devtools`, `tailscale`. A
  new recipe goes to the index repository (`novusedge/stoat-recipes`, spec
  3) as its own repository.
- `novusedge/stoat-recipe-template`: `recipe.toml` and `install.sh` from
  the annotated sample (spec 2), a `README`, and a workflow that runs
  `stoat recipe lint`.
- `stoat recipe lint [dir]`: manifest in `Reject` mode; `shellcheck` on
  every `.sh`; `set -e` present; the live-vs-disk block present for a
  recipe without `os` restrictions; every param has a type and a default
  or `required`; every `[scripts]` key names a known guest or alias;
  `depends` names exist. Exit 1 on any finding, one line each.
- `stoat recipe test <name> --os <os>[,<os>…]`: for each OS, create a
  throwaway VM from the catalog under a temp `STOAT_HOME`, `up`, `apply`
  the recipe, run its health check, `rm`. Prints a matrix and exits 1 on
  any failure. Needs KVM; `doctor` says so.
- The index accepts a recipe PR only with the lint output and a `recipe
  test` matrix pasted from the PR template.

## CI

`ci.yml` gains:

- `shellcheck` over `internal/recipes/bundled/**/*.sh`, `scripts/`, and
  `.githooks/`.
- `golangci-lint` with a config that enables `errcheck`, `staticcheck`,
  `govet`, `unused`, `misspell`, and nothing that argues about style.
- `stoat recipe lint` over every bundled recipe.
- The Python job is removed with spec 4.

`e2e.yml`: `workflow_dispatch` only, runs `scripts/e2e.sh` and `recipe
test` for the bundled four on a runner with KVM. Documented as the gate
for the `needs-live-boot` label.

`dependabot.yml`: gomod weekly, github-actions weekly.

`just setup` runs `just hooks`.

## Repository files

- `.github/PULL_REQUEST_TEMPLATE.md`: what changed, why, tests run, OS
  matrix for a recipe or guest change, docs updated.
- `.github/ISSUE_TEMPLATE/`: `bug.yml` (stoat version, host distro, guest
  OS, `stoat doctor` output, log tail), `recipe.yml` (recipe request or
  report), `guest.yml` (guest OS request with image URL and init system).
- `CODEOWNERS`: `NovusEdge` for everything; `internal/recipes/bundled/`
  and `docs/recipes/` listed so a recipe change requests review by name.
- `SECURITY.md`: report by email, 90-day disclosure, what counts (a
  bundled script that pipes a download into a root shell counts).
- `CODE_OF_CONDUCT.md`: Contributor Covenant 2.1.

## Code conventions

Added to `docs/design/core-api.md`, applied by every plan:

- A CLI command's `--json` data is a `wire` struct, never an inline
  `map[string]any`.
- `a.fail` for an error from `core`; `a.failMsg` for a condition the CLI
  detects itself.
- A destructive command uses one `confirm` helper: `-y` skips, `--json`
  and `--quiet` refuse, a TTY prompts.
- A kong command that aliases another uses `aliases:` and one `toArgs`
  case.
- Every new TOML file type decodes through `tomlx`.

## Plans and workflows

- Every feature spec gets one plan in `docs/plans/` (tracked; the
  `docs/superpowers/` directory is gitignored and stays local).
- A plan is executed by a workflow of sonnet agents, one branch per
  plan, one PR per plan, squash merged.
- Order: guest definitions, recipe contract, remote recipes, MCP in Go,
  project file. The process changes here land first, in their own PR, so
  the workflows run under the new gate.

## Testing the process

- `shellcheck` and `golangci-lint` pass on `main` before the CI change
  merges; findings fixed in the same PR.
- `recipe lint` has a fixture directory per finding.
- `recipe test` has a unit test against a fake `core` and one e2e run in
  `e2e.yml`.
- Templates validated by GitHub's form schema on push.
