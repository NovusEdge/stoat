"""fastmcp tool definitions: the MCP surface over the stoat CLI.

Every tool here does three things and nothing else: run the deterministic
guards in guards.py, call Client.run (or Client itself, for allow_exec
checks), and return the result's `data` dict verbatim. If a tool ever needs
to compute something stoat's JSON contract does not already hand back, that
is a sign the layering is wrong (docs/design/mcp-server.md, "Layering"), and
the fix belongs in the Go core, not here.

Four things are enforced independently of what an MCP client does, because
the protocol guarantees none of them: rate limiting (RateLimiter.check,
first thing every tool does), path confinement (guards.check_host_path),
name validation (guards.check_vm_name), and allow_exec (checked here, since
core.Exec deliberately does not enforce it and core is a library the TUI and
CLI also call without wanting that restriction).

Deliberately absent, not merely gated, per docs/design/mcp-server.md §4:
no `share` parameter on any tool, no bring-your-own image path, no
`recipe new`, no `ssh-command`, no VM-less `logs`.
"""

from __future__ import annotations

import functools
import sys
from collections.abc import Callable, Sequence
from typing import Any, Literal, TypeVar

from fastmcp import FastMCP
from fastmcp.exceptions import ToolError

from .client import Client, argv_for_bool
from .errors import ContractMismatch, GuardRejection, StoatCrashed, StoatError
from .guards import RateLimiter, check_host_path, check_image_id, check_vm_name, strip_forbidden

mcp: FastMCP = FastMCP("stoat")

# One bucket set for the whole process. Not thread-safe by design: the stdio
# transport is single-threaded, see RateLimiter's own doc comment.
_RATE_LIMITER = RateLimiter()

# Set by main() once the binary is resolved and the contract check passes.
# Left None at import time on purpose, so importing this module (as the test
# suite does, to inspect tool schemas) never touches a subprocess.
_client: Client | None = None


def get_client() -> Client:
    """Return the process-wide Client, constructing it on first use.

    Tests that only inspect tool registration never call this. A tool
    actually being invoked without main() having run (e.g. calling a tool
    function directly in a unit test) gets a freshly constructed Client,
    which still resolves $STOAT_BIN/PATH correctly on its own.
    """
    global _client
    if _client is None:
        _client = Client()
    return _client


F = TypeVar("F", bound=Callable[..., Any])


def _guarded(tool_name: str) -> Callable[[F], F]:
    """Rate-limit by tool name, then translate our exceptions to ToolError.

    ToolError's message reaches the client regardless of fastmcp's
    mask_error_details setting (fastmcp/server/server.py re-raises any
    FastMCPError, ToolError's base class, untouched); a bare exception would
    be masked whenever that setting is on. GuardRejection and StoatError are
    both normal, expected outcomes ("no such VM", "path escapes the
    sandbox"), not bugs, so their messages are meant to be seen.

    functools.wraps sets __wrapped__, which inspect.signature follows, so
    fastmcp still reads the ORIGINAL function's signature (and therefore
    still builds the right JSON schema) through this wrapper.
    """

    def decorator(fn: F) -> F:
        @functools.wraps(fn)
        def wrapper(*args: Any, **kwargs: Any) -> Any:
            _RATE_LIMITER.check(tool_name)
            try:
                return fn(*args, **kwargs)
            except GuardRejection as e:
                raise ToolError(str(e)) from e
            except StoatError as e:
                raise ToolError(f"{e.code}: {e.message}") from e
            except StoatCrashed as e:
                raise ToolError(f"stoat exited {e.returncode} without answering") from e

        return wrapper  # type: ignore[return-value]

    return decorator


def _flag(name: str, value: str | int | None) -> list[str]:
    """One `--name value` pair, or nothing if value was not given."""
    if value is None:
        return []
    return [f"--{name}", str(value)]


def _recipes_flag(recipes: Sequence[str] | None) -> list[str]:
    """`--recipes a,b,c`, `--recipes ""` to clear, or nothing if omitted.

    None means "leave alone" (the flag is absent from argv). An explicit
    empty list means "clear the recipe list", which the CLI spells as
    `--recipes` given an empty value (cli.md: "empty clears it").
    """
    if recipes is None:
        return []
    return ["--recipes", ",".join(recipes)]


def _require_exec_allowed(vm: str) -> None:
    """Refuse exec/copy_to/copy_from on a VM created with --allow-exec=false.

    core.Exec does not enforce this itself (core is a library the TUI and
    CLI also call, and a blanket refusal there would be the wrong layer);
    docs/design/mcp-server.md §1.2 is explicit that enforcement is this
    server's job. One extra `get` call is the cost of not trusting the
    caller's own claim about a VM it does not control.
    """
    data = get_client().run("get", vm)
    if not data.get("vm", {}).get("allow_exec", True):
        raise ToolError(f"exec is disabled on {vm!r} (created with --allow-exec=false)")


# ---------------------------------------------------------------------------
# Read-only
# ---------------------------------------------------------------------------


@mcp.tool(
    name="list_vms",
    description=(
        "List every VM stoat manages, one entry per VM: name, OS, mode, state, "
        "resources, disk, share, ssh port, recipes, and port forwards. Includes "
        "VMs whose vm.toml failed to parse, shown with state 'broken' and an "
        "error message, rather than hiding them. Read-only: touches no VM and "
        "changes nothing on the host."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("list_vms")
def list_vms() -> dict[str, Any]:
    return get_client().run("ls")


@mcp.tool(
    name="vm_status",
    description=(
        "Show one VM's full status: OS, mode, backend, state, CPUs, RAM, disk, "
        "share, ssh port and user, recipes, port forwards, and whether exec is "
        "allowed on it. Read-only: touches no VM and changes nothing on the host."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("vm_status")
def vm_status(vm: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("get", vm)


@mcp.tool(
    name="list_images",
    description=(
        "List what stoat can build a VM from: the catalog of known images, plus "
        "anything already downloaded locally. Read-only: does not download "
        "anything and changes nothing on the host."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("list_images")
def list_images() -> dict[str, Any]:
    return get_client().run("images")


@mcp.tool(
    name="list_recipes",
    description=(
        "List recipes stoat knows about, optionally filtered to ones applicable "
        "to a guest OS and/or backend. Reads the recipes directory only; does "
        "not touch any VM or run anything. Read-only."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("list_recipes")
def list_recipes(os: str | None = None, backend: str | None = None) -> dict[str, Any]:
    argv = ["recipes", *_flag("os", os), *_flag("backend", backend)]
    return get_client().run(*argv)


@mcp.tool(
    name="check_recipes",
    description=(
        "Report, for each named recipe, why it would NOT apply to a given "
        "guest OS/backend; an empty issue list means every one of them would "
        "apply. Does not run anything or touch any VM. Read-only."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("check_recipes")
def check_recipes(
    recipes: list[str], os: str, backend: str | None = None
) -> dict[str, Any]:
    argv = ["check-recipes", *recipes, "--os", os, *_flag("backend", backend)]
    return get_client().run(*argv)


@mcp.tool(
    name="logs",
    description=(
        "Tail one VM's log: its qemu console output (default), or its most "
        "recent recipe-apply log. Always scoped to a single named VM; there is "
        "no way to read stoat's own global log through this tool. Read-only."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("logs")
def logs(vm: str, which: Literal["console", "apply"] = "console", n: int = 50) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("logs", vm, "--which", which, "-n", str(n))


@mcp.tool(
    name="doctor",
    description=(
        "Check host prerequisites: qemu/KVM, qemu-img, ssh, xorriso and "
        "/dev/kvm. Always succeeds at checking (the JSON result's 'healthy' "
        "field carries an unhealthy host, rather than raising). Read-only: "
        "changes nothing on the host."
    ),
    annotations={"readOnlyHint": True, "destructiveHint": False},
)
@_guarded("doctor")
def doctor() -> dict[str, Any]:
    return get_client().run("doctor")


# ---------------------------------------------------------------------------
# Mutating
# ---------------------------------------------------------------------------


@mcp.tool(
    name="create",
    description=(
        "Create a new VM from a catalog image, without starting it. Only "
        "catalog image ids are accepted (see list_images); a bring-your-own "
        "image path, a console password, or a host share cannot be set through "
        "this tool. Memory is 'ram_mb' and is in MEGABYTES; 'disk' is a size "
        "string like '8G'. Reversible: the VM can be deleted with destroy. "
        "Mutating: creates a new VM directory and disk under the stoat data root."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("create")
def create(
    name: str,
    image: str,
    os: str | None = None,
    backend: str | None = None,
    mode: str | None = None,
    ram_mb: int | None = None,
    cpus: int | None = None,
    disk: str | None = None,
    recipes: list[str] | None = None,
    allow_exec: bool = True,
) -> dict[str, Any]:
    name = check_vm_name(name)
    image = check_image_id(image)
    argv = [
        "create",
        name,
        "--image",
        image,
        *_flag("os", os),
        *_flag("backend", backend),
        *_flag("mode", mode),
        *_flag("ram", ram_mb),
        *_flag("cpus", cpus),
        *_flag("disk", disk),
        *_recipes_flag(recipes),
        *argv_for_bool("allow-exec", allow_exec),
    ]
    return get_client().run(*argv)


@mcp.tool(
    name="start",
    description=(
        "Start a VM (boots qemu). Reversible with stop. Mutating: consumes "
        "host CPU, RAM and, once running, a forwarded ssh port."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("start")
def start(vm: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("up", vm)


@mcp.tool(
    name="stop",
    description=(
        "Stop a running VM gracefully. Reversible with start. Mutating: shuts "
        "qemu down; refuses if the VM is not already running."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("stop")
def stop(vm: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("down", vm)


@mcp.tool(
    name="apply_recipes",
    description=(
        "Run a VM's own configured recipes over ssh (or, optionally, a subset "
        "of them). Mutating: runs arbitrary recipe scripts inside the guest, "
        "which is only reversible to whatever extent the recipe itself is."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("apply_recipes")
def apply_recipes(vm: str, only: list[str] | None = None) -> dict[str, Any]:
    vm = check_vm_name(vm)
    argv = ["apply", vm]
    for name in only or []:
        argv += ["--only", name]
    return get_client().run(*argv)


@mcp.tool(
    name="update",
    description=(
        "Change a stopped VM's RAM, CPU count, ssh port, disk size (grow-only), "
        "or recipe list. Only the fields actually passed are changed; a share "
        "cannot be set through this tool even if included by mistake, since it "
        "is stripped before the request is built. Mutating; most fields take "
        "effect only at the VM's next start."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("update")
def update(
    vm: str,
    ram_mb: int | None = None,
    cpus: int | None = None,
    ssh_port: int | None = None,
    disk: str | None = None,
    recipes: list[str] | None = None,
) -> dict[str, Any]:
    vm = check_vm_name(vm)
    # Built as a generic map and run through strip_forbidden even though this
    # tool's own signature has no `share` parameter to begin with: §4's rule
    # is that the patch itself is what gets sanitized, in case a future
    # caller builds one from a VM object it read back (guards.strip_forbidden
    # docstring explains why that is the reasonable case to guard against).
    patch: dict[str, object] = {}
    if ram_mb is not None:
        patch["ram"] = ram_mb
    if cpus is not None:
        patch["cpus"] = cpus
    if ssh_port is not None:
        patch["ssh_port"] = ssh_port
    if disk is not None:
        patch["disk"] = disk
    if recipes is not None:
        patch["recipes"] = recipes
    patch = strip_forbidden(patch)

    argv = ["update", vm]
    argv += _flag("ram", patch.get("ram"))  # type: ignore[arg-type]
    argv += _flag("cpus", patch.get("cpus"))  # type: ignore[arg-type]
    argv += _flag("ssh-port", patch.get("ssh_port"))  # type: ignore[arg-type]
    argv += _flag("disk", patch.get("disk"))  # type: ignore[arg-type]
    if "recipes" in patch:
        argv += _recipes_flag(patch["recipes"])  # type: ignore[arg-type]
    return get_client().run(*argv)


@mcp.tool(
    name="clone",
    description=(
        "Copy a VM: a fresh overlay disk and a fresh ssh port, but NOT the "
        "source's port forwards. Refuses a running source. Reversible with "
        "destroy on the clone. Mutating: creates a new VM."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("clone")
def clone(source: str, name: str) -> dict[str, Any]:
    source = check_vm_name(source)
    name = check_vm_name(name)
    return get_client().run("clone", source, name)


@mcp.tool(
    name="snapshot",
    description=(
        "Save a disk snapshot of a VM under a tag. Needs a disk to snapshot; "
        "a live-mode VM has none and this refuses. Reversible: restore rolls "
        "back to it, and the tag can be removed by a human with `stoat "
        "snapshot --delete`. Mutating: writes a new qemu snapshot."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("snapshot")
def snapshot(vm: str, tag: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("snapshot", vm, tag)


@mcp.tool(
    name="restore",
    description=(
        "Roll a VM's disk back to a previously saved snapshot tag, discarding "
        "everything written since. Mutating and only reversible if another "
        "snapshot was taken after the one being restored to."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("restore")
def restore(vm: str, tag: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("snapshot", vm, tag, "--restore")


@mcp.tool(
    name="forward",
    description=(
        "Show, set, or clear a VM's host:guest port forwards. With no pairs "
        "and clear=false, only shows the current forwards. Setting or clearing "
        "on a running VM saves the change but it only takes effect at the "
        "VM's next start. Mutating when pairs are given or clear=true."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("forward")
def forward(vm: str, pairs: list[str] | None = None, clear: bool = False) -> dict[str, Any]:
    vm = check_vm_name(vm)
    argv = ["forward", vm]
    if clear:
        argv.append("--clear")
    else:
        argv += list(pairs or [])
    return get_client().run(*argv)


@mcp.tool(
    name="wait",
    description=(
        "Block until a VM reaches a state: reachable (sshd answering), applied "
        "(most recent recipe run finished), or stopped (qemu no longer "
        "running). The bound is 'timeout_seconds', a plain integer count of "
        "seconds, not a duration string. Fails immediately, rather than waiting "
        "out the timeout, for a state the VM can never reach. Mutating only in "
        "that it blocks the caller; it does not itself change the VM."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": False},
)
@_guarded("wait")
def wait(
    vm: str,
    until: Literal["reachable", "applied", "stopped"] = "reachable",
    timeout_seconds: int = 120,
) -> dict[str, Any]:
    vm = check_vm_name(vm)
    argv = ["wait", vm, "--until", until, "--timeout", f"{timeout_seconds}s"]
    # The subprocess-level timeout must exceed stoat's own --timeout, or the
    # process gets killed before stoat has a chance to answer with "timeout"
    # itself; the 15s margin covers process startup and JSON flush.
    return get_client().run(*argv, timeout=timeout_seconds + 15)


# ---------------------------------------------------------------------------
# Destructive
# ---------------------------------------------------------------------------


@mcp.tool(
    name="destroy",
    description=(
        "Permanently delete a VM's directory and disk. Refuses while the VM "
        "is running. NOT reversible: there is no undo, and a snapshot taken "
        "before deletion goes with it. Always confirmed on this tool's behalf "
        "(-y), since --json never prompts."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": True},
)
@_guarded("destroy")
def destroy(vm: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    return get_client().run("rm", vm, "-y")


@mcp.tool(
    name="prune",
    description=(
        "Report stale files stoat can clean up: partial downloads always; "
        "broken VM directories if broken=true; orphaned images if images=true. "
        "Dry-run by default and reports only; pass apply=true to actually "
        "delete, which is NOT reversible for whatever it removes."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": True},
)
@_guarded("prune")
def prune(apply: bool = False, broken: bool = False, images: bool = False) -> dict[str, Any]:
    argv = ["prune"]
    if apply:
        argv.append("--apply")
    if broken:
        argv.append("--broken")
    if images:
        argv.append("--images")
    return get_client().run(*argv)


# ---------------------------------------------------------------------------
# Execution
# ---------------------------------------------------------------------------


@mcp.tool(
    name="exec",
    description=(
        "Run a command inside a VM over ssh and return its stdout, stderr and "
        "exit code. The command runs with whatever privileges the guest's ssh "
        "user has; effects inside the guest are whatever the command itself "
        "does, and are not automatically reversible. Refused on a VM created "
        "with --allow-exec=false. Reaches outside this process (openWorldHint)."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
)
@_guarded("exec")
def exec_cmd(vm: str, command: list[str]) -> dict[str, Any]:
    vm = check_vm_name(vm)
    _require_exec_allowed(vm)
    return get_client().run("exec", vm, "--", *command)


@mcp.tool(
    name="copy_to",
    description=(
        "Copy a file from the host into a VM's guest filesystem. The host "
        "path must resolve under that VM's own shared directory "
        "(~/.stoat/shared/<vm>/); anything else is refused before stoat ever "
        "runs. Refused on a VM created with --allow-exec=false. Overwrites "
        "whatever was at the destination in the guest; not reversible from "
        "here. Reaches outside this process (openWorldHint)."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
)
@_guarded("copy_to")
def copy_to(vm: str, local: str, remote: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    _require_exec_allowed(vm)
    resolved_local = check_host_path(local, vm)
    data = get_client().run("cp", "--vm", vm, "--direction", "to", "--local", resolved_local, "--remote", remote)
    _verify_cp_local(data, resolved_local)
    return data


@mcp.tool(
    name="copy_from",
    description=(
        "Copy a file out of a VM's guest filesystem to the host. The host "
        "destination path must resolve under that VM's own shared directory "
        "(~/.stoat/shared/<vm>/); anything else is refused before stoat ever "
        "runs. Refused on a VM created with --allow-exec=false. Overwrites "
        "whatever was at the destination on the host; not reversible from "
        "here. Reaches outside this process (openWorldHint)."
    ),
    annotations={"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
)
@_guarded("copy_from")
def copy_from(vm: str, remote: str, local: str) -> dict[str, Any]:
    vm = check_vm_name(vm)
    _require_exec_allowed(vm)
    resolved_local = check_host_path(local, vm)
    data = get_client().run("cp", "--vm", vm, "--direction", "from", "--local", resolved_local, "--remote", remote)
    _verify_cp_local(data, resolved_local)
    return data


def _verify_cp_local(data: dict[str, Any], authorised_local: str) -> None:
    """Post-verify that `cp` acted on the exact path this server authorised.

    `cp` echoes back the resolved absolute local path it used (json.md's
    `cp` row). Guard-time validation establishes that the path we ASKED for
    was safe; this establishes that the path stoat actually ACTED on is the
    same one, catching any divergence between the two resolvers (guard-time
    uses os.path.realpath, stoat's own resolveLocal does not) rather than
    trusting them to agree.
    """
    actual = data.get("local")
    if actual != authorised_local:
        raise ToolError(
            f"cp acted on {actual!r}, which does not match the authorised path "
            f"{authorised_local!r}; refusing to report this as successful"
        )


def main() -> None:
    """Console entry point: resolve the binary, check the contract, serve.

    A contract mismatch or missing binary is a startup failure with a plain
    message, not a traceback three tools into a session (docs/design/mcp-
    server.md §2, "Startup contract check").
    """
    global _client
    try:
        client = Client()
        client.check_contract()
    except (FileNotFoundError, ContractMismatch) as e:
        print(f"stoat-mcp: {e}", file=sys.stderr)
        raise SystemExit(1) from None
    _client = client
    mcp.run()


if __name__ == "__main__":
    main()
