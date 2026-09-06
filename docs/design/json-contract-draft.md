# stoat CLI structured output: the MCP API boundary

**Status:** historical proposal, written 2026-08-04. The Python MCP wrapper
described below has been replaced by the Go server in the Stoat binary.
Use the current [JSON contract](../reference/json.md) and
[MCP reference](../reference/mcp.md) when building integrations.

**Historical premise (for the 2026-08-04 proposal):** the MCP server was Python
with fastmcp in a separate process. It reached `internal/core` only by executing
the `stoat` binary and reading its output. Therefore this document specifies an
**API**, not a display format. Anything a Python caller had to regex, guess at,
or reconstruct from two streams was a defect in the API, not a rough edge.

The proposal was read against the 2026-08-04 tree: `internal/cli/cli.go` (18
subcommands), `internal/core/*.go` (14 typed errors, 9 return types),
`docs/design/core-api.md` §9/§10, and the then-stale `docs/reference/cli.md`
(see §8.6).

---

## 1. Flag or subcommand?

### Recommendation: a **global `--json` flag**, pre-scanned from argv before `Parse`.

```
stoat --json ls
stoat ls --json          # also accepted
stoat --json create work --image alpine-virt
```

**Why not `stoat api <cmd>`.** It doubles the dispatch surface. Every command
would exist twice, once in the human tree and once in the api tree, and the two
would drift, which is the exact failure `internal/core` was built to prevent
(`cli.go:1-12` states this as the package's reason for existing). It also
invites the api tree to grow commands the human tree does not have, and then
the human CLI stops being the thing under test. The one genuine advantage, that
an unknown subcommand under `api` still knows to answer in JSON, is obtainable
more cheaply (below).

**Why not per-command `--json`.** It is the same flag registered 18 times, and
it fails on the case that matters most: a **usage error**. Today `Parse`
returns `usageError` for an unknown subcommand or a bad flag *before* any
`FlagSet` exists. If `--json` lives in a subcommand's `FlagSet`, then
`stoat frobnicate --json` and `stoat up --json` (missing VM name) both exit 2
with plain prose on stderr, and a machine consumer that mistyped a command
gets an unparseable answer for precisely the class of error it most needs to
distinguish. A global flag scanned from raw argv is immune.

**Why not `STOAT_JSON=1`.** Convenient for a subprocess spawner, but an
environment variable that reformats output is invisible in the command line you
copy into a terminal to debug, and a user who exports it in their shell rc gets
a broken human CLI with no visible cause. If the MCP server wants insurance
against forgetting the flag, it should build argv from one helper function.
**Do not add the env var.**

### The argv scan, and the one hard case

`--json` is recognized anywhere in argv **except** after `exec`'s VM name.
`exec` deliberately parses no flags of its own (`cli.go:371-387`) because
everything after the VM name is the guest's command, verbatim. So:

| Command | `--json` goes to |
|---|---|
| `stoat --json exec work ls -la` | stoat |
| `stoat exec --json work ls -la` | stoat |
| `stoat exec work ls --json` | **the guest** (untouched) |

Implementation: scan argv left to right, consuming `--json` until the token
that is `exec`'s VM name (i.e. for `exec`, stop after index 1). Everything
else scans the whole argv. This is one function, `splitJSONFlag([]string)
(bool, []string)`, unit-testable with no side effects, and it must be pinned by
a test that `stoat exec work ls --json` sends `--json` to the guest.

### What `--json` implies

- **Non-interactive, always.** `rm` without `-y` does not prompt; it fails with
  error code `confirmation_required` (§2). An enforcement boundary that can be
  bypassed by a process that answers a prompt is not a boundary.
- **No progress chatter on stdout.** All human prose (`starting work...`,
  `port forwards were not copied; set them with: ...`) is suppressed. Anything
  worth saying is a field.
- **No ANSI color**, regardless of `colorEnabled()`.
- **`-q` becomes meaningless** and is accepted as a no-op, not an error.

---

## 2. The envelope

### One line, one JSON object, always on stdout

Every `--json` invocation emits **JSON Lines on stdout**: zero or more
non-terminal events, then **exactly one terminal `result` line**, always last.
A non-streaming command emits exactly one line total. This means the streaming
and non-streaming cases have *the same* consumer loop, which is the whole point
(§4).

**Success:**

```json
{"v":1,"type":"result","cmd":"up","ok":true,"data":{"vm":{"name":"work","os":"alpine","mode":"live","backend":"apkovl","state":"running","cpus":4,"ram_mb":4096,"disk":"8G","share":"","recipes":[],"ssh_port":2222,"ssh_user":"root","installed":false,"forwards":[]}}}
```

**Failure:**

```json
{"v":1,"type":"result","cmd":"up","ok":false,"error":{"code":"not_found","message":"not found: work","subject":"work"}}
```

**Usage error (exit 2):**

```json
{"v":1,"type":"result","cmd":"","ok":false,"error":{"code":"usage","message":"unknown subcommand \"frobnicate\""}}
```

Rules:

- `data` and `error` are **mutually exclusive**. `ok` is redundant with which
  one is present, and that is deliberate: a consumer that branches on `ok` and
  a consumer that branches on `"error" in obj` must never disagree.
- `data` is **always an object, never an array or scalar**. `ls` returns
  `{"vms":[...]}`, not `[...]`. An array has nowhere to add a field later.
- `cmd` is the resolved subcommand, `""` when argv never named a valid one.
- Terminal type is `"result"` for both success and failure. There is exactly
  one terminal condition for the consumer to look for.

### Error codes

One stable snake_case code per typed error in `internal/core`. This is the
whole reason core returns typed errors; do not spend it on prose matching.

| Code | Go sentinel | Where it comes from |
|---|---|---|
| `not_found` | `core.ErrNotFound` | no such VM, no such catalog image id |
| `broken` | `core.ErrBroken` | `vm.toml` exists but will not parse |
| `name_taken` | `core.ErrNameTaken` | `Create`/`Clone` |
| `invalid_spec` | `core.ErrInvalidSpec` | bad ram/cpus/disk/port/tag/recipe args |
| `image_not_downloaded` | `core.ErrImageNotDownloaded` | `Create` against an unfetched catalog entry |
| `recipe_not_applicable` | `core.ErrRecipeNotApplicable` | `Create`, `Apply --only` |
| `not_running` | `core.ErrNotRunning` | `Stop`, `Exec`, `CopyTo/From`, `Apply` |
| `already_running` | `core.ErrAlreadyRunning` | `Start`, `Destroy`, `Update` disk-grow |
| `no_disk` | `core.ErrNoDisk` | snapshot ops on a live-mode VM |
| `immutable_field` | `core.ErrImmutableField` | `Update` on name/os/backend/mode |
| `disk_shrink` | `core.ErrDiskShrink` | `Update` disk smaller than current |
| `cannot_reach` | `core.ErrCannotReach` | `Wait` on a state that is impossible |
| `applied_at_boot` | `core.ErrAppliedAtBoot` | `Apply` on a cloudinit VM |
| `unknown_log` | `core.ErrUnknownWhich` | bad `--which` |
| `timeout` | `context.DeadlineExceeded` | `Exec`, `Apply`, `Wait`, `pull` |
| `canceled` | `context.Canceled` | signal during a long op |
| `usage` | `cli.usageError` | every `Parse` failure |
| `confirmation_required` | *(new, CLI-only)* | `rm` without `-y` under `--json` |
| `internal` | anything else | unwrapped `qemu-img:` output, IO errors, panic |

**Mapping is an ordered `errors.Is` chain, first match wins**, in a single
function with a single table. Order matters in exactly one place: `Update`'s
disk-grow refusal wraps `ErrAlreadyRunning` (`update.go:252`), so it reports
`already_running` rather than a disk-specific code. That is truthful: the
refusal *is* "stop it first". But the consumer needs to know which field
caused it, which is what `error.subject` is for.

### `error.subject`: supplied, not parsed

Core wraps with `fmt.Errorf("%w: %s", ErrNotFound, name)`, so the subject is
inside the message and **not machine-extractable**. Do not regex it back out.
The CLI already knows the subject at the call site (`a.VM`, `a.Tag`, the field
name in a `Patch`), so it supplies it:

```json
{"code":"immutable_field","message":"immutable field: os","subject":"os","kind":"field"}
```

`kind` is one of `vm`, `field`, `recipe`, `image`, `snapshot`, `port`, `path`,
or absent. Optional, additive, and it costs one literal per call site.

### Adding a new error kind without breaking consumers

Three rules, stated as the compatibility promise:

1. **Codes are only ever added.** A code is never renamed, never repurposed,
   never removed. If a distinction is later needed inside `invalid_spec`, it
   arrives as a *new* code and `invalid_spec` stops being emitted for that
   case, which is a breaking change and requires a `v` bump (§6). Splitting a
   code is the one thing this rule does not make free; say so rather than
   pretending otherwise.
2. **Consumers MUST treat an unrecognized code as a generic failure**, never
   crash on it. Document this as a `MUST` in the contract. fastmcp code
   should have `except KeyError` nowhere near this.
3. **`internal` is the escape hatch.** Any error that has no mapping is
   `internal` with the Go error string in `message`. It is never absent, so a
   consumer always gets a well-formed envelope even for a failure nobody
   anticipated.

---

## 3. Per-command shapes

### 3.1 DTOs, not struct tags on core types, and why

**Recommendation: an explicit `internal/cli/wire` package holding the wire
types and the marshalling from core types.** Not `json:"..."` tags on
`core.VM`, `core.CatalogImage`, `core.Snapshot`, etc.

Four reasons, in order of weight:

1. **`core.VM.Paths` must not be serialized at all.** It carries five absolute
   host paths (`Dir`, `Disk`, `ConsoleLog`, `ApplyLog`, `VNCSocket`,
   `MonitorSocket`, `vm.go:69-76`). §10.3's first deterministic rule is "no
   host paths as arguments"; handing an agent six of them in every `ls`
   response is how it learns paths to feed back into `cp`. With struct tags,
   omitting `Paths` means either `json:"-"` on a field the TUI legitimately
   wants, or a second type anyway.
2. **Adding a field to `core.VM` would silently change the wire format.** Core
   is under active development by three concurrent agents right now. With tags,
   the next field someone adds ships to every MCP consumer with no review of
   whether it should be public. With a DTO, exposing a field is an edit to the
   wire package: a deliberate act with a diff.
3. **Go field names are the wrong public contract.** `CPUs`, `RAM`, `OS`,
   `SSHPort`, `Exact`, `VMState` are idiomatic Go and bad JSON.
   `default` marshalling gives `{"CPUs":4,"RAM":4096}`; tags would give
   `{"cpus":4,"ram":4096}` where `ram` has an undocumented unit. The wire type
   says `ram_mb`. Units belong in names when the reader is a machine that will
   do arithmetic.
4. **Some core types are display-shaped and must be labelled as such.**
   `core.Snapshot.Size` is "203 MiB" and `Created` is "2026-08-04 12:00:00",
   both scraped from qemu's table (`snapshot.go:187-210`). Tagging them
   `json:"size"` publishes free-form qemu output as a contract. The wire type
   names them `size_display` and `created_display` so nobody parses them, and
   flags the follow-up.

Cost, stated honestly: ~9 conversion functions, all trivial and all
golden-file-testable. That is the price of the boundary being a boundary.

**Naming convention:** `snake_case`, units suffixed (`_mb`, `_bytes`, `_ms`),
booleans unprefixed (`downloaded`, not `is_downloaded`), empty slices emitted
as `[]` never `null` (Go emits `null` for a nil slice, so this needs an explicit
`if s == nil { s = []T{} }` in the wire constructors, and a test, because it is
the single most common Go-to-JSON bug and it turns `for f in vm["forwards"]`
into a `TypeError`).

### 3.2 The VM DTO

```json
{
  "name": "work",
  "os": "alpine",
  "mode": "live",
  "backend": "apkovl",
  "state": "running",
  "cpus": 4,
  "ram_mb": 4096,
  "disk": "8G",
  "share": "/home/u/src",
  "recipes": ["xfce.alpine.sh"],
  "ssh_port": 2222,
  "ssh_user": "root",
  "installed": false,
  "forwards": [{"host_port": 8080, "guest_port": 80}]
}
```

Deliberately absent:

- **`paths`** (see above). If a human debugging path ever needs it, add
  `stoat get <vm> --paths` as an opt-in that the MCP server simply never calls.
  Do not make it the default.
- **`console_password`**, not on `core.VM` today (only on `config.VM`), and
  the wire type must never grow it. Worth a test asserting the marshalled
  output of a cloud VM contains no `password` substring.
- **`error`**, present only when `state == "broken"`, as
  `"error": "broken vm.toml: oldvm: toml: line 4: ..."`. Note this string can
  contain a host path from the TOML decoder; that is acceptable (it is the
  user's own `~/.stoat`) but it is the one place a path can leak into `ls`.

`share` **is** a host path and **is** retained: it is part of what the VM is,
the TUI shows it, and hiding it would make `ls` lie. It is flagged in §7 as
something the MCP server must never accept as an *input*.

### 3.3 Every command

Existing commands:

| `cmd` | `data` |
|---|---|
| `ls` | `{"vms":[VM,…]}` |
| `create` | `{"vm":VM}` |
| `up` | `{"vm":VM}` (state re-read after `Start`, so `state` is authoritative) |
| `down` | `{"vm":VM}` |
| `rm` | `{"name":"scratch","deleted":true}` |
| `clone` | `{"vm":VM,"source":"work","forwards_copied":false}` |
| `images` | `{"images":[Image,…]}` |
| `pull` | streaming; final `{"id":"alpine-virt","downloaded":true}` |
| `prune` | `{"dry_run":true,"items":[PruneItem,…]}` |
| `snapshot` (list) | `{"vm":"work","snapshots":[Snapshot,…]}` |
| `snapshot` (save/restore/delete) | `{"vm":"work","tag":"clean","action":"restore"}` |
| `cp` | `{"vm":"work","direction":"to_guest","local":"/home/u/f","remote":"/tmp/f"}` |
| `forward` (show) | `{"vm":"work","forwards":[…],"active":true}` |
| `forward` (set/clear) | `{"vm":"work","forwards":[…],"active":false,"applies_at":"next_start"}` |
| `exec` | `{"vm":"work","exit_code":1,"stdout":"…","stderr":"…"}` |
| `provision` | streaming; final `{"vm":"work","provisioned":true,"skipped_reason":""}` |
| `logs` | `{"lines":["…"]}` (stoat's own log) |
| `recipe list` | `{"dir":"/home/u/.stoat/recipes","recipes":["xfce.alpine.sh",…]}` |
| `recipe new` | `{"path":"/home/u/.stoat/recipes/foo.alpine.sh"}` |
| `doctor` | `{"healthy":false,"checks":[Check,…]}` |
| `version` | `{"version":"1.2.3","contract":1}` |
| `help` | `{"usage":"usage: stoat …"}` |
| `ssh` | **refused** under `--json`, see below |

```json
Image      {"id":"alpine-virt","os":"alpine","variant":"virt","backend":"apkovl",
            "file":"alpine-virt-3.24.1-x86_64.iso","downloaded":true,
            "bytes":62914560,"bytes_exact":true,"byo":false}
Snapshot   {"tag":"clean","vm_state":true,"size_display":"203 MiB",
            "created_display":"2026-08-04 12:00:00"}
Check      {"name":"qemu-img","ok":false,"detail":"not found",
            "fix":["sudo","pacman","-S","qemu-img"]}
PruneItem  {"class":"orphaned_image","path":"/home/u/.stoat/isos/old.iso"}
```

Call-outs where the internal shape is wrong as a contract:

- **`Image.byo`** is new. Today `runImages` infers BYO from `ID == ""` and
  overwrites the state column (`cli.go:719-723`). Making the consumer re-derive
  "empty id means bring-your-own" from a missing field is exactly the kind of
  implicit rule that gets reimplemented wrong. Emit it.
- **`Image.file`** is a **bare filename under `isos/`, never absolute**
  (`images.go:37-41` guarantees this). Safe to emit, and worth documenting as
  a guarantee so the MCP server can rely on it.
- **`PruneItem`** does not exist in core. `core.Prune` returns
  `[]string` of `"orphaned image: /abs/path"` (`prune.go:163,216,317`), three
  hard-coded prefixes. Splitting them back apart in the CLI is string surgery
  on a format core owns, which is the anti-pattern this whole document is
  against. **Recommend a small core follow-up: `Prune` returns
  `[]PruneItem{Class, Path}`** and the human renderer formats the prefix. Until
  that lands, the CLI splits on the three known prefixes with a test pinning
  all three, and `class` falls back to `"other"` with the whole string in
  `path`. Flag it as debt, do not pretend it is clean.
- **`forward.applies_at`** is the field that must exist. `core.Forward` returns
  `active bool` and its doc comment (`forward.go:26-35`) is emphatic that
  "saved but not yet live" must never look like "refused". Flattening that into
  a bare success is precisely the bug it warns about. `applies_at` is `"now"`
  or `"next_start"`.
- **`doctor.healthy`**, not `ok`: the envelope already owns `ok` and two
  differently-scoped `ok` fields one level apart is a trap. See §5 for the
  exit-code consequence.
- **`provision.skipped_reason`** carries the cloud short-circuit
  (`cli.go:1050-1057`), which today is a prose line and an exit 0. A consumer
  must be able to tell "recipes ran" from "there was nothing to run" without
  reading English.
- **`ssh`** cannot produce an envelope: it `syscall.Exec`s and the process
  image is gone (`cli.go:1021-1038`). Under `--json` it is a usage error with
  a message naming `ssh-command`. Do not fake a result.

New commands for the new core operations:

| `cmd` | invocation | `data` |
|---|---|---|
| `get` | `stoat get <vm>` | `{"vm":VM}` |
| `update` | `stoat update <vm> --ram 8192 --cpus 8` | `{"vm":VM,"changed":["ram","cpus"],"applies_at":"next_start"}` |
| `wait` | `stoat wait <vm> --until reachable --timeout 120s` | `{"vm":"work","until":"reachable","reached":true,"waited_ms":4210}` |
| `apply` | `stoat apply <vm> [--only a,b]` | streaming; final `{"vm":"work","applied":["xfce.alpine.sh"]}` |
| `recipes` | `stoat recipes --os alpine --backend apkovl` | `{"recipes":[{"name":"xfce.alpine.sh","label":"xfce","target_os":"alpine","shared":false}]}` |
| `check-recipes` | `stoat check-recipes --os alpine --backend apkovl a,b` | `{"applicable":false,"issues":[{"name":"xfce.cloud.yaml","reason":"…"}]}` |
| `ssh-command` | `stoat ssh-command <vm>` | `{"argv":["ssh","-p","2222",…]}` |
| `logs <vm>` | `stoat logs <vm> --which console` | `{"vm":"work","which":"console","lines":[…]}` |

Notes:

- **`update`'s `changed` list** matters more than it looks. `core.Update` takes
  a `Patch` of pointers so "not set" differs from "set to zero"
  (`update.go:30-37`). The CLI must reproduce that distinction: a flag that
  was *given* sets the pointer, and a flag that was absent leaves it nil. That
  means using `fs.Visit` to walk *set* flags, not comparing against zero
  values. Getting this wrong makes `stoat update work --ram 8192` silently
  clear `share`. This is the single highest-risk conversion in the plan.
- **`check-recipes.applicable`** is `len(issues) == 0`, emitted explicitly.
  `CheckRecipes` returns only the failures (`apply.go:255-258`), so an empty
  list is the success answer. But a consumer reading `{"issues":[]}` and
  guessing is a consumer that will one day guess wrong.
- **`logs <vm>`** overloads today's `logs` (which tails *stoat's own* log,
  `cli.go:1148`). With a positional VM name it reads that VM's log via
  `core.Logs`; without one it keeps today's behaviour. This is the cheapest way
  to give the MCP taxonomy's `logs` tool a real backing command; the alternative
  (`stoat vm-logs`) adds a subcommand whose name exists only to dodge a
  collision. Note the collision explicitly in the docs either way.
- **`ssh-command`'s argv contains the identity file's absolute host path.**
  It is worth having for humans and for the TUI; the MCP server should not
  expose it as a tool. Flagged in §7.

---

## 4. Streaming

### JSON Lines, all on stdout, one terminal `result`.

```
{"v":1,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":41943040,"total":62914560,"percent":66}}
{"v":1,"type":"progress","cmd":"pull","data":{"id":"alpine-virt","done":62914560,"total":62914560,"percent":100}}
{"v":1,"type":"result","cmd":"pull","ok":true,"data":{"id":"alpine-virt","downloaded":true}}
```

```
{"v":1,"type":"stage","cmd":"apply","data":{"vm":"work","recipe":"xfce.alpine.sh","index":1,"total":2}}
{"v":1,"type":"log","cmd":"apply","data":{"vm":"work","stream":"stdout","line":"Unpacking libx11-data..."}}
{"v":1,"type":"stage","cmd":"apply","data":{"vm":"work","recipe":"devtools.alpine.sh","index":2,"total":2}}
{"v":1,"type":"result","cmd":"apply","ok":true,"data":{"vm":"work","applied":["xfce.alpine.sh","devtools.alpine.sh"]}}
```

**Event types:** `progress` (byte counts), `stage` (a named step started),
`log` (one line of subprocess output), `result` (terminal, exactly one, always
last). Consumers **MUST ignore unknown `type` values** and **MUST NOT** assume
any non-terminal event occurs at all: a fast download may emit zero
`progress` lines, and a quiet recipe zero `log` lines.

### Why JSON Lines, and what was rejected

- **Concatenated JSON with no separator.** Requires an incremental parser.
  Python's stdlib `json` has none (`raw_decode` with manual offset tracking is
  the workaround, and it is a workaround). Rejected.
- **Server-Sent Events.** Framing designed for HTTP, needs an `event:`/`data:`
  parser nobody has in a subprocess pipe, and the payload is still JSON
  underneath. Rejected as strictly more work for the same result.
- **Length-prefixed frames.** Requires reading the pipe as binary and
  hand-managing buffers; kills `for line in proc.stdout`. Rejected.
- **A progress line on stderr, result on stdout.** See below; rejected hard.

JSONL is `for line in proc.stdout: obj = json.loads(line)`. Nothing beats it.

**One requirement it imposes:** no literal newline may appear inside a line.
Go's `encoding/json` escapes `\n` inside strings, so `exec`'s multi-line guest
output is safe by construction. Use `json.Encoder` with `SetEscapeHTML(false)`
and one `Encode` per event (it appends exactly one `\n`), and **flush after
every event**: a buffered writer that flushes at exit makes streaming
pointless and makes a hung command look silent rather than progressing.

### stdout / stderr separation

**Everything structured goes to stdout, including errors.** stderr under
`--json` carries only things that are *not* part of the contract: Go panic
traces, and stray output from `qemu-img`/`ssh` that escapes capture.

Justification, in order:

1. A consumer that must merge two streams to reconstruct one result will
   eventually interleave them wrong. There is no ordering guarantee between two
   pipes.
2. `subprocess` with two pipes and naive sequential reads **deadlocks** when
   either fills its buffer. Recommending "read stdout, then stderr" is
   recommending a hang on any command with large `exec` output.
3. The error envelope is a *result*, not a diagnostic. It is the answer to the
   question that was asked.

The consumer contract is therefore: **`stdout=PIPE`, `stderr=DEVNULL` (or a
separately-drained pipe used only for crash forensics), never parsed.**

This is a real behavioural change from today, where every failure path writes
`fmt.Fprintln(stderr, "stoat: up:", err)`. Under `--json` those calls are
replaced, not duplicated.

**Provision/apply output must be wrapped, not passed through.** Today
`runProvision` copies raw log bytes to stdout (`cli.go:1067`, `copyNew`). Under
`--json` that would inject non-JSON into the JSONL stream and corrupt it for
every consumer. Each newly-appended line becomes a `{"type":"log",…}` event.
This is the one streaming conversion with real work in it.

### The panic guarantee

A `defer recover()` in `Main` emits a terminal
`{"type":"result","ok":false,"error":{"code":"internal","message":"panic: …"}}`
and returns exit 1. Combined with "exactly one terminal line", the consumer
gets: **either a `result` line, or the process died**. The second case is
detectable as "exit != 0 and no result line seen".

### Non-UTF-8 guest output

`exec`'s `stdout`/`stderr` are arbitrary guest bytes. Go's `json.Marshal`
silently replaces invalid UTF-8 with U+FFFD, lossy and silent, which is the
bad combination. **Recommendation:** when the captured bytes are not valid
UTF-8, emit `stdout_base64` instead of `stdout` and set
`"encoding":"base64"` (default `"utf8"`). The consumer branches on `encoding`.

This is a judgement call and I flag it as one: the alternative (always base64)
makes every `exec` result unreadable to a human debugging the pipe, which is a
real daily cost against a rare failure. The conditional form is slightly more
consumer code for much better ergonomics. If the team prefers uniformity,
always-base64 is defensible; always-lossy-UTF-8 is not.

---

## 5. Exit codes

### Recommendation: keep 0 / 1 / 2 exactly as they are. Add nothing.

The envelope carries the error kind with more precision than a byte, and adding
an exit code per error kind would break the §2 promise that a *new* error kind
is additive: every new kind would need a new number, and the numbers would run
out of meaning long before they ran out of range. `git`, `docker` and `kubectl`
all converged on the same answer for the same reason.

Two `--json`-only changes, both deliberate:

1. **`exec` no longer passes through the guest's exit status.** Today it
   returns `res.ExitCode` as the process exit code (`cli.go:911`), and its own
   doc comment admits the cost: a guest exiting 2 is indistinguishable from a
   stoat usage error. For a human running `stoat exec vm make test && deploy`
   that trade is right. For a machine consumer it is fatal, since the guest status
   is already in `data.exit_code`, so conflating the two throws away the one
   thing the envelope exists to give. Under `--json`: **exit 0 if the command
   ran at all**, whatever it returned; exit 1 only if stoat could not run it
   (`not_running`, `not_found`, ssh missing). Human mode is untouched.

2. **`doctor` exits 0 with `healthy:false`.** Exit 1 means "stoat failed to do
   what you asked". `doctor` succeeded at checking; the *host* is unhealthy,
   which is the answer, not a failure to produce one. Human mode keeps exit 1
   (documented in `docs/reference/cli.md`).

   Counter-argument, stated: this makes `stoat --json doctor` and
   `stoat doctor` disagree on exit code, and someone will be surprised once.
   I still recommend it, because the alternative, `ok:true` in the envelope
   alongside exit 1, is a contradiction a consumer has to be told to ignore,
   and told-to-ignore rules are the ones that get forgotten.

**Usage errors keep exit 2 and now also emit an envelope** (`code:"usage"`).
That is only possible because `--json` is pre-scanned (§1).

Consumer pattern, for the contract doc:

```python
proc = subprocess.run(argv, stdout=PIPE, stderr=DEVNULL)
result = None
for line in proc.stdout.splitlines():
    obj = json.loads(line)
    if obj.get("type") == "result":
        result = obj
if result is None:
    raise StoatCrashed(proc.returncode)      # died before answering
if not result["ok"]:
    raise StoatError(result["error"]["code"], result["error"]["message"])
return result["data"]
```

Note what this does **not** do: branch on `returncode` for anything except
"was there a result at all". That is the intended shape.

---

## 6. Versioning

### `"v": 1` on **every** line: an integer contract version, not the build version.

Per-line, not per-invocation, because a streaming consumer may hand individual
events to different code paths and each should be self-describing. It costs
five bytes.

`stoat --json version` returns `{"version":"1.4.0","contract":1}` so a consumer
can check the contract before doing anything else. The MCP server should assert
`contract == 1` at startup and refuse to run against an unknown one, rather
than discovering the mismatch mid-tool-call.

### The compatibility promise, stated as rules

**A consumer MAY rely on:**

- `v`, `type`, `ok`, `cmd` being present on every line.
- exactly one `type:"result"` line, and it being last.
- `error.code` being one of the documented set *or* an unrecognized string.
- documented fields in `data` for a given `cmd` existing, with the documented
  type and unit.
- `[]` for an empty list. Never `null`, never absent.

**A consumer MUST tolerate:**

- new fields appearing anywhere, at any time, without a version bump.
- new `error.code` values.
- new `type` values (ignore them).
- new subcommands.
- `error.subject` / `error.kind` being absent.

**A consumer MUST NOT rely on:**

- field ordering, or key ordering within an object.
- `error.message` text. It is for humans and it will change.
- the *absence* of a field meaning anything.
- `size_display` / `created_display` formats. Opaque qemu output, never parse.
- stderr content, or its emptiness.
- exit codes beyond `{0, 1, 2}`.
- human-mode (non-`--json`) output, at all, ever. It is not a contract.

**`v` bumps to 2 only for a removal or a meaning change**: a field deleted, a
field's unit changed, an error code split or repurposed, `result` stopping
being last. Additions never bump it. When it does bump, the CLI accepts
`--json-version=1` to keep emitting the old shape for one release cycle.
**Reserve that flag name now; do not implement it.** Building a version
negotiation mechanism before there is a second version is the kind of work that
guarantees the first version was designed wrong.

---

## 7. What not to expose

### 7.1 Surfaces that hand an agent a host write path

Ranked by severity. Enforcement lives in the MCP server (§10.1); this section
says what the server must cover, and what the CLI must do to make covering it
possible.

| # | Surface | What it grants | MCP rule |
|---|---|---|---|
| 1 | `create --share DIR` | **arbitrary host directory, read-write, into the guest.** `security_model=none` today (§10.2). An agent with `exec` on such a VM writes anywhere the user can. | Do not expose `share` as a tool parameter at all. If needed later, accept only a name resolved under `~/.stoat/shared/<vm>/`, never a path. |
| 2 | `update --share DIR` | identical to #1, on an existing VM, and it is `Patch.Share` so it is trivially reachable from a generic map of JSON args (`update.go:38-48` anticipates exactly this caller). | Same. Strip `share` from any patch built from agent input. |
| 3 | `cp <local> <vm>:<remote>` | arbitrary host **read** into a guest, arbitrary host **write** out of one. `core.CopyTo/From` explicitly do not sandbox (`copy.go:60-64`). | Resolve after `filepath.EvalSymlinks`, require the prefix `~/.stoat/shared/<vm>/`, reject otherwise. |
| 4 | `create --image /abs/path.qcow2` | arbitrary host file read, booted as a disk. `resolveImage` accepts an absolute BYO path. | Accept catalog IDs only. |
| 5 | `recipe new <name>` | writes a file into `~/.stoat/recipes/` and returns its path; that file is then executed inside a guest by `apply`. | Do not expose. Recipe authoring is a human act. |
| 6 | `logs` (no VM) | stoat's own log, which contains every VM name and operation the user has run. | Low severity, but it is unrelated to any VM the agent was given. Expose per-VM logs only. |
| 7 | `ssh-command <vm>` | argv including the absolute path to `id_stoat`. | Do not expose as a tool. |

### 7.2 Output that leaks host structure

- `core.VM.Paths`: six absolute paths per VM. **Omitted from the wire type**
  (§3.2). This is the single biggest output-side win and it costs nothing.
- `prune` items: absolute paths. **Retained**: they are inside the data root
  and reporting *what would be deleted* is the entire operation. An agent that
  can call `prune` can already delete them.
- `recipe list --json` → `dir`, the recipes directory. Retained; it is one
  path the user chose, and `recipe list` is meaningless without it. Not
  exposed as an MCP tool (see #5 above).
- `VM.share`: retained as output (§3.2), forbidden as input (#1, #2). The
  asymmetry is the point: telling an agent a share exists is fine; letting it
  choose one is not.
- `VM.error` for broken VMs may contain a path from the TOML decoder.
  Accepted.

### 7.3 What the CLI must do so enforcement stays possible

1. **Host paths must be identifiable by flag, never smuggled inside a compound
   argument.** `cp`'s `<vm>:<path>` spelling (`cli.go:316-335`) is exactly that
   smuggling: to know which side is a host path, the MCP server must
   reimplement the colon-splitting, including the `srcRemote == dstRemote`
   rejection, and any divergence is a hole. **Recommendation: under `--json`,
   `cp` takes explicit flags:**
   `stoat --json cp --vm work --direction to --local /p --remote /q`.
   The scp spelling stays for humans. This is the most important single item
   in §7, because it converts "the server must parse stoat's argument syntax
   correctly" into "the server passes a path in a flag named `--local`".
2. **Never resolve host paths in a way the server cannot predict.**
   `config.expand` resolves a leading `~`; `Share` is stored as given. The CLI
   must **echo back the resolved absolute path it acted on** (`cp`'s `local`
   field), so the server can post-verify that what it authorised is what
   happened. Pre-validation plus post-verification catches the case where the
   two disagree.
3. **`--json` must never prompt** (§1). A boundary a subprocess can talk its
   way past is not one.
4. **No new command may take a host path without a dedicated flag.** Write it
   into the contract doc as a rule for future contributors, since the person
   adding the 20th subcommand will not have read this document.

---

## 8. Implementation plan

The collision point is real: `internal/cli/cli.go` is 1243 lines and every
subcommand lives in it. Four agents editing it concurrently produce four
conflicting diffs over the same `switch`.

### The answer to the collision problem: split the file first, as a pure move.

Before any JSON work, one agent splits `cli.go` with **no logic change**:

| File | Contents |
|---|---|
| `cli.go` | package doc, `Args`, `Parse`, `usage()`, `Main`, exit codes |
| `run_vm.go` | `runLS`, `runCreate`, `runUp`, `runDown`, `runRM`, `runClone` |
| `run_image.go` | `runImages`, `runPull`, `humanSize` |
| `run_access.go` | `runExec`, `runCopy`, `runSSH`, `runProvision`, `streamFile`, `copyNew` |
| `run_state.go` | `runSnapshot`, `runForward`, `parseForwards`, `runPrune`, `runDoctor` |
| `run_misc.go` | `runLogs`, `tailLines`, `runRecipe`, `colorEnabled`, `colorState`, `colorize`, `oneLine` |

Reviewable with `git diff -M --stat` showing pure renames. After this, phase-2
agents own a file each and never touch the same one. `Parse` remains a single
serialized bottleneck. Accept that and give it to one agent (phase 1).

### Ordered plan

| # | Work | Effort | Mechanical vs judgement | Parallel? |
|---|---|---|---|---|
| **0** | Split `cli.go` as above. Zero logic change. | trivial | mechanical | no, blocks all |
| **1** | `internal/cli/wire`: envelope types, `Emit`, error-code table, `splitJSONFlag`. No `cli.go` edits at all. | low | **judgement**: the code table and its ordering are the contract; get it wrong and every consumer inherits it | no, blocks 3+ |
| **2** | `internal/cli/wire`: DTO types + `From*` constructors for VM, Image, Snapshot, Check, PruneItem, Forward, ExecResult. Golden-file tests including nil-slice→`[]` and the no-`console_password` assertion. | low | mostly mechanical; judgement on field names/units, decided in §3 | yes, alongside 1 (different files) |
| **3** | `cli.go` only: `Args.JSON`, pre-scan wiring, `Main`'s error/usage/panic paths, a `printer` value threaded into every `runX` signature. Signature change only, bodies untouched. | medium | judgement on the printer shape; the rest mechanical | no, single-file bottleneck |
| **4** | Convert `runX` bodies, one agent per `run_*.go`. | medium | mechanical, high volume | **yes, 5 agents** |
| **5** | Streaming: `pull` progress events; `provision` log-line wrapping (`copyNew` → line splitter → `log` events). | medium | judgement: progress throttling, partial-line handling at the tail of `copyNew` | yes, 2 agents (different files) |
| **6** | `exec` exit-code change + non-UTF-8 handling; `doctor` exit-code change. | low | **judgement**: both change documented behaviour under `--json` | yes |
| **7** | New subcommands: `get`, `wait`, `ssh-command`, `recipes`, `check-recipes`, `logs <vm>`, `cp` explicit flags. One file each, `Parse` cases batched into one agent. | medium | mechanical once §3 is fixed | partly, `Parse` cases serialize |
| **8** | `update` subcommand. | medium | **judgement, highest risk**: `fs.Visit` for set-vs-zero (§3.3); a bug here silently clears fields | no, do it alone, with tests first |
| **9** | `apply` subcommand + stage events. Depends on core.Apply landing. | medium | judgement: stage boundaries come from `"=== recipe NAME ==="` markers in the log, which is a scrape | no |
| **10** | `core.Prune` → `[]PruneItem`. Small core change removing the prefix-string scrape. | low | mechanical | yes, independent of all CLI work |
| **11** | `docs/reference/json.md`, the contract. `docs/reference/cli.md` rewrite. | medium | judgement: this document *is* the API | yes |
| **12** | End-to-end: every subcommand under `--json` produces exactly one `result` line and valid JSON. Table-driven. | low | mechanical | yes |

**Critical path:** 0 → 1 → 3 → 4. Everything else hangs off it.

**Max useful parallelism:** 5 agents at phase 4, plus 10 and 11 running the
whole time from the start.

### 8.6 Two findings outside the brief, reported not fixed

1. **`docs/reference/cli.md` is badly stale.** It documents 10 subcommands;
   `Parse` handles 18. `create`, `images`, `pull`, `clone`, `prune`,
   `snapshot`, `cp`, `forward`, `exec` and `recipe` are entirely undocumented,
   and the `doctor` section describes the old `qemu.Preflight` implementation
   rather than `core.Doctor`. Whatever "documented CLI contract that must not
   be broken" means, it currently covers barely half the surface. Phase 11 must
   fix this, and until it does, the human-mode contract is whatever `cli.go`
   does.
2. **`core.Prune`'s `[]string` with formatted prefixes** is the one place core
   returns display strings where structured data was meant (§3.3, phase 10).
   `core.Snapshot.Size`/`Created` are the second (§3.1). Neither blocks this
   work; both make the wire layer do string surgery it should not have to.

---

## 9. Open questions, flagged, not decided

- **Non-UTF-8 `exec` output** (§4). I recommend conditional base64; uniform
  base64 is defensible. Not my call to settle alone.
- **`doctor`'s exit code under `--json`** (§5). I recommend 0; the counter-
  argument is real and I have stated it.
- **Whether `Paths` should be available at all**, behind an opt-in flag. I
  recommend omitting entirely for now: an opt-in that exists will eventually
  be called by something.
- **Rate limiting** is an MCP-server `MUST` per §10.3. Nothing in the CLI
  contract helps or hinders it; noting only that it is not addressed here.
- **`stoat --json help`** returning the usage text as a string is a placeholder.
  A structured command list would be more useful to an agent doing discovery,
  but nothing needs it yet, so I have not designed one.
