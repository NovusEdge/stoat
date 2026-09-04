# Recipe contract v3

Spec 2 of 4. Depends on the guest definitions spec (prelude, `tomlx`).
Status: approved 2026-09-04.

## Goal

A recipe declares what it needs (params), what it produced (outputs), and
how to tell it works (health). A machine caller reads the schema before an
apply and the result after. Today a recipe is a script with a name, and the
only result is "applied".

## Scope

In: typed params, per-VM param storage, secrets, outputs, one health check
per recipe, the `[cmd]` verbs deferred from spec 1 (download, useradd),
annotated sample files, and comments on stoat-written fields.

Out: lifecycle hooks (pre-boot, pre-stop). Deferred until params, outputs,
and health ship. Remote recipes: spec 3.

## Manifest

```toml
schema = 3                     # recipe.toml without it is schema 2 and still loads

[params.user]
type     = "string"            # string | int | bool | enum | secret
default  = "dev"
help     = "account added to the docker group"

[params.channel]
type    = "enum"
values  = ["stable", "test"]
default = "stable"

[params.authkey]
type     = "secret"
required = true
help     = "tailscale auth key"

[outputs]
socket = "path of the docker socket"

[health]
check   = "docker info"
timeout = "30s"
```

Rules:

- A param name matches `[a-z][a-z0-9_]*`. The guest sees it as
  `STOAT_PARAM_<NAME>` upper-cased, plus `STOAT_RECIPE=<recipe name>`.
- `required` defaults to false. A param with neither `default` nor
  `required = true` is an error at manifest load.
- `int` renders as decimal; `bool` as `true`/`false`; `enum` must be one of
  `values`, and `default` must too.
- `secret` has no `default`. The manifest declares the slot only.
- `[outputs]` maps a name to help text. The script writes `name=value`
  lines to the file at `$STOAT_OUTPUT`. A line with an undeclared name is a
  warning in the apply log and is stored anyway. A declared name the script
  did not write is stored as absent.
- `[health]` is one command run through the prelude as the recipe's ssh
  user with `escalate`. Exit 0 is healthy. `timeout` defaults to `30s`.
- Recipes with `schema = 2` or no `schema` keep loading; they have no
  params, outputs, or health.

The verbs deferred from spec 1 join the guest file and prelude here:

```toml
# guest.toml
[cmd]
download = "fetch -o"          # curl is absent from FreeBSD and Alpine base
useradd  = "pw useradd -n {name} -m"
```

Prelude: `stoat_download`, `stoat_useradd`. Same template rules as `[svc]`.

## Storage

```toml
# vm.toml
[recipes.docker]
user    = "dev"
channel = "stable"

# written by stoat; do not edit
[applied.docker]
version = "1.2.0"
hash    = "sha256:…"
at      = "2026-09-04T10:00:00Z"
outputs = { socket = "/var/run/docker.sock" }
health  = "ok"                 # ok | failed | unknown
```

- `[recipes.<name>]` holds every non-secret param the user set. Unset
  params fall back to the manifest default at apply time and are not
  written.
- `[applied.<name>]` is today's `Applied` map plus `outputs` and `health`.
  The encoder writes the `# written by stoat; do not edit` comment above
  every stoat-owned table.
- `hash` covers the script body, the resolved non-secret params in sorted
  order, and the names of set secrets. A changed param re-applies a
  `run = "once"` recipe; `plan_recipes` reports `run (params changed)`.
- Secrets live in `secrets.toml` next to `vm.toml`, mode 0600, as
  `<recipe>.<param> = "…"`. `vm.toml` never carries a secret value. The
  loader refuses a `secrets.toml` with mode wider than 0600.
- Every reader of a VM renders a secret param as `<set>` or `<unset>`:
  `--json`, MCP, the TUI, `logs`, `stoat get`.

## Delivery to the guest

- ssh path: the params go in the environment of the recipe's ssh session
  (`env STOAT_PARAM_…= sh -s`). Secrets are in that environment only for
  the script's lifetime and never in the apply log.
- cloud-init path: non-secret params go into the wrapped script's
  environment. Secrets are written by `write_files` to
  `/run/stoat/secrets.env` with mode 0600, sourced by the wrapper, and
  deleted by the last `runcmd` entry.
- `$STOAT_OUTPUT` is a temp file the wrapper creates; `Provision` reads it
  back over ssh after the script exits, the cloud-init path reads it from
  the marker directory.

## Health

- `apply` runs the health check for every recipe it ran, after the last
  one and after the reboot if any. A failing check sets
  `health = "failed"`, and `apply` exits 1 with the check's output as the
  error message. Recipes that ran keep their `applied` entry, so the next
  apply does not rerun them.
- `stoat wait --healthy` polls every applied recipe's check until all pass
  or the longest `timeout` elapses, and names the first failing recipe.
- `vm_status` reports `recipes[]` with `name, applied, health, outputs`.
- A VM with no health-declaring recipes reports `health = "unknown"` and
  `--healthy` returns as soon as ssh is reachable.

## CLI

- `stoat new --set docker.user=dev --secret docker.authkey`: `--secret`
  prompts on a TTY, and reads `STOAT_SECRET_DOCKER_AUTHKEY` otherwise.
- `stoat update --set k=v --unset k --secret k`.
- `stoat recipe show <name>`: params, outputs, health. `--json` emits
  `wire.RecipeSchema`.
- `stoat apply --dry-run` gains the reason `params changed`.
- `stoat wait --healthy`.
- `stoat recipe new` scaffolds from the annotated sample.

Wire: `wire.Recipe` gains `params{}`, `outputs{}`, `health{check, timeout}`;
`wire.VMStatus` gains `recipes[]`; every param value in `wire` passes
through a redactor keyed on the manifest type.

## Samples

`docs/reference/samples/vm.toml`, `recipe.toml`, `guest.toml`: one file
each with every field present and a comment on each field giving type,
default, and who writes it. `recipe new` copies `recipe.toml` and fills
name and OS. `docs/reference/` links them from the format pages. A test
decodes each sample with `tomlx` in `Reject` mode, so a sample cannot drift
from the struct.

## Encoder

`vm.toml` needs a comment above each stoat-owned table. BurntSushi's
encoder has no comment tag. Two options, decided in the plan after a
check of the current release:

1. go-toml/v2 `comment:"…"` struct tag. The `tomlx` helper wraps decode
   and encode, so the switch touches one file plus the `MetaData` use in
   `config.go:213`.
2. Keep BurntSushi and render `vm.toml` from a template.

Option 1 is the default. Option 2 stands only if the tag is missing or
behaves badly with inline tables.

## TUI

The recipe picker gains a params form rendered from the schema with huh
fields by type: input for string and int, confirm for bool, select for
enum, password input for secret. Required fields block confirm.

## Errors

| Condition | Message |
|---|---|
| unknown param | `docker: no param "usr"; has user, channel, authkey` |
| type mismatch | `docker.channel: "beta" is not one of stable, test` |
| required unset | `docker.authkey: required secret is unset; run stoat update --secret docker.authkey` |
| secrets.toml mode | `secrets.toml: mode 0644, want 0600` |
| health failed | `docker: health check failed after 30s: <last line of output>` |
| output undeclared | apply log: `docker: output "sock" is not declared` |

## Testing

- Manifest goldens per param type, and loads of a schema-2 manifest.
- Hash covers params and secret names; changing a secret value does not
  change the hash; changing a non-secret does.
- `secrets.toml` written 0600; loader refuses 0644.
- Redaction: `TestJSONEnvelopeEveryCommand` fixture VM carries a secret
  with a sentinel value; every command's JSON output is scanned for it.
- Health: fake ssh returns exit 1; `apply` exits 1, `applied` entry kept,
  `health = "failed"`.
- Samples decode in `Reject` mode.
- e2e: `docker` with `user = "dev"` on alpine, then `wait --healthy`, then
  `vm_status` shows the socket output.
