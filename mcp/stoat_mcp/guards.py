"""The deterministic blocks.

Pure functions, no I/O beyond path resolution, so they are exhaustively
testable. Every one is enforced regardless of what the MCP client does,
because the protocol guarantees nothing useful here: human-in-the-loop is a
SHOULD, tool annotations are explicitly advisory, and elicitation is a
capability a client may simply not declare. What stoat wants guaranteed,
stoat enforces.

See docs/design/mcp-server.md §4 and json-contract-draft.md §7.
"""

from __future__ import annotations

import os
import re
import time
from pathlib import Path

from .errors import GuardRejection

# A VM name is a DIRECTORY name under the data root. That is the whole reason
# this is strict: the name becomes a path component, and stoat resolves every
# operation by directory. Letters, digits, dash, underscore, dot, but never a
# leading dot (which would hide it and collide with stoat's own bookkeeping)
# and never "." or ".." (which are traversal).
_VM_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")

# Catalog image IDs look like "alpine-virt" or "ubuntu-24.04". Anything with a
# separator is a path, and a path is what section 7.1 #4 forbids: an absolute
# BYO path is an arbitrary host file read, booted as a disk.
_IMAGE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")

_RECIPE_NAME = re.compile(r"^[a-z][a-z0-9-]*$")
_RECIPE_REF = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]*$")


def check_vm_name(name: str) -> str:
    """Reject anything that is not a plain VM name.

    A name reaches stoat as a directory component, so "../../etc" or an
    absolute path would escape the data root. Rejecting by pattern rather
    than by sanitizing is deliberate: there is no correct way to "fix" a
    malicious name, and a sanitizer that silently rewrites input hides the
    attempt.
    """
    if not isinstance(name, str) or not name.strip():
        raise GuardRejection("vm name is required")
    if name != name.strip():
        raise GuardRejection(f"vm name {name!r} has leading or trailing whitespace")
    if name in (".", ".."):
        raise GuardRejection(f"vm name {name!r} is a path traversal")
    if os.sep in name or (os.altsep and os.altsep in name):
        raise GuardRejection(f"vm name {name!r} contains a path separator")
    if "\x00" in name:
        raise GuardRejection("vm name contains a null byte")
    if not _VM_NAME.match(name):
        raise GuardRejection(
            f"vm name {name!r} is not allowed: use letters, digits, dot, dash "
            "and underscore, starting with a letter or digit"
        )
    return name


def check_image_id(image: str) -> str:
    """Catalog IDs only. A path is refused outright.

    `create --image /abs/path.qcow2` is an arbitrary host file read booted as
    a disk (json-contract-draft.md section 7.1 #4). The CLI accepts it because
    a human bringing their own image is a supported workflow; an agent
    choosing one is not.
    """
    if not isinstance(image, str) or not image.strip():
        raise GuardRejection("image id is required")
    if os.sep in image or (os.altsep and os.altsep in image) or image.startswith("~"):
        raise GuardRejection(
            f"image {image!r} looks like a path. Only catalog image ids are "
            "accepted; run list_images to see them."
        )
    if not _IMAGE_ID.match(image):
        raise GuardRejection(f"image id {image!r} is not a valid catalog id")
    return image


def check_index_name(ref: str) -> str:
    """Accept an index recipe name with an optional safe git ref.

    The recipe name is always resolved through the curated index. A ref may
    contain slashes for branches such as ``feature/topic`` but cannot contain
    URL, option, or path-traversal syntax.
    """
    if not isinstance(ref, str) or not ref or ref != ref.strip():
        raise GuardRejection("recipe index name is required")
    if ref.startswith("-") or "\\" in ref or "\x00" in ref:
        raise GuardRejection(f"recipe index name {ref!r} is not safe")
    if ref.count("@") > 1:
        raise GuardRejection(f"recipe index name {ref!r} has more than one ref")
    name, separator, branch = ref.partition("@")
    if not _RECIPE_NAME.fullmatch(name):
        raise GuardRejection(
            f"recipe index name {ref!r} must start with a letter and contain only letters, digits and dashes"
        )
    if not separator:
        return ref
    if not _RECIPE_REF.fullmatch(branch) or branch.startswith("/") or branch.endswith("/"):
        raise GuardRejection(f"recipe ref {branch!r} is not safe")
    parts = branch.split("/")
    if ".." in branch or any(
        part in ("", ".", "..")
        or part.startswith(".")
        or part.endswith(".")
        or part.endswith(".lock")
        for part in parts
    ):
        raise GuardRejection(f"recipe ref {branch!r} is not safe")
    return ref


def shared_dir(vm: str, data_root: str | os.PathLike[str] | None = None) -> Path:
    """The one host directory an agent may read or write for this VM.

    Mirrors the layout stoat itself uses for the writable 9p export
    (~/.stoat/shared/<vm>/), which is what makes a copy in or out equivalent
    to the guest writing into its own work share.
    """
    root = Path(data_root) if data_root else Path(os.environ.get("STOAT_HOME") or Path.home() / ".stoat")
    return (root / "shared" / check_vm_name(vm)).resolve()


def check_host_path(path: str, vm: str, data_root: str | os.PathLike[str] | None = None) -> str:
    """Confine a host path to ~/.stoat/shared/<vm>/, resolving symlinks first.

    The order matters and is the whole guard: resolve, THEN compare. A check
    performed before resolution is defeated by a symlink inside the sandbox
    pointing out of it, and such a symlink is trivially created by the guest,
    which has that same directory mounted read-write.

    Comparison is by path PARTS, not by string prefix. "~/.stoat/shared/work"
    is a string prefix of "~/.stoat/shared/work-evil", so a prefix test would
    admit a sibling directory belonging to a different VM.

    This check is possible here, unlike for the 9p share itself, precisely
    because stoat performs the copy: a 9p operation is served by QEMU and
    stoat never sees it.
    """
    if not isinstance(path, str) or not path.strip():
        raise GuardRejection("path is required")
    if "\x00" in path:
        raise GuardRejection("path contains a null byte")

    sandbox = shared_dir(vm, data_root)
    # expanduser first: "~" is the user's, not ours, and os.path.realpath
    # would treat a literal "~" as a relative directory name.
    candidate = Path(os.path.expanduser(path))
    if not candidate.is_absolute():
        raise GuardRejection(
            f"path {path!r} must be absolute, under {sandbox}"
        )
    resolved = Path(os.path.realpath(candidate))

    if resolved != sandbox and sandbox not in resolved.parents:
        raise GuardRejection(
            f"path {path!r} resolves to {resolved}, which is outside this VM's "
            f"shared directory ({sandbox}). Only files under that directory can "
            "be copied in or out."
        )
    return str(resolved)


class RateLimiter:
    """A token bucket per tool name, plus one bucket every tool shares.

    The MCP spec makes rate limiting a server MUST. The numbers here are a
    starting point rather than a tuned value: generous enough that ordinary
    agent work never notices, tight enough that a runaway loop calling
    `create` a thousand times stops being the host's problem.

    The shared bucket is what bounds the server as a whole. Per-tool buckets
    alone let a caller burst `capacity` times against each of ~20 tools, so
    the real ceiling was 20x the number anyone read off this class.

    Not thread-safe on purpose; the stdio server is single-threaded, and a
    lock here would imply a concurrency story that does not exist.
    """

    def __init__(
        self,
        capacity: int = 30,
        refill_per_second: float = 0.5,
        total_capacity: int = 60,
        total_refill_per_second: float = 2.0,
    ) -> None:
        if capacity < 1 or total_capacity < 1:
            raise ValueError("capacity must be at least 1, or nothing can ever run")
        if refill_per_second <= 0 or total_refill_per_second <= 0:
            # A bucket that never refills is a bucket that permanently bricks
            # the tool after `capacity` calls. Refusing at construction beats
            # discovering it in production, and it is why the message below
            # can divide by this without a guard.
            raise ValueError("refill_per_second must be positive")
        self.capacity = capacity
        self.refill_per_second = refill_per_second
        self.total_capacity = total_capacity
        self.total_refill_per_second = total_refill_per_second
        self._buckets: dict[str, tuple[float, float]] = {}

    def check(self, tool: str, now: float | None = None) -> None:
        """Consume one token for `tool` and one shared token.

        Both buckets are read before either is charged. A call refused by one
        must not spend from the other: a hot tool hitting its own limit would
        otherwise drain the shared bucket and starve every other tool.
        """
        now = time.monotonic() if now is None else now
        tool_tokens = self._read(tool, self.capacity, self.refill_per_second, now, tool)
        total_tokens = self._read(
            "", self.total_capacity, self.total_refill_per_second, now, "the server"
        )
        self._buckets[tool] = (tool_tokens - 1.0, now)
        self._buckets[""] = (total_tokens - 1.0, now)

    def _read(
        self, key: str, capacity: int, refill: float, now: float, subject: str
    ) -> float:
        """Return key's token count at `now`, raising when it is below one."""
        tokens, last = self._buckets.get(key, (float(capacity), now))
        tokens = min(capacity, tokens + (now - last) * refill)
        if tokens < 1.0:
            wait = (1.0 - tokens) / refill
            raise GuardRejection(
                f"rate limit reached for {subject}; retry in about {wait:.0f}s"
            )
        return tokens


def check_flag_free(values: list[str], what: str) -> list[str]:
    """Refuse a value that kong would read as a flag.

    `forward` and `check_recipes` splat their list arguments into argv as
    positionals. `forward(pairs=["--clear"])` reached kong as the --clear
    flag and wiped the VM's forwards, from a call that passed clear=False.
    Nothing escapes the process (there is no shell), so this is argv
    confusion, and refusing a leading dash closes it.
    """
    for v in values:
        if not isinstance(v, str) or not v.strip():
            raise GuardRejection(f"{what} contains an empty value")
        if v.startswith("-"):
            raise GuardRejection(f"{what} value {v!r} may not start with a dash")
    return values


def strip_forbidden(patch: dict[str, object]) -> dict[str, object]:
    """Remove parameters an agent may never set, from a patch it supplied.

    `share` is the one that matters: it grants an arbitrary host directory,
    read-write, into a guest, and core.Patch is exactly the generic-map shape
    that makes it reachable from JSON tool arguments
    (json-contract-draft.md section 7.1 #2).

    Dropped silently rather than rejected, because an agent copying a VM
    object it read back into an update call is doing something reasonable;
    the field simply has no effect. Reading `share` is fine, setting it is not,
    and that asymmetry is deliberate.
    """
    return {k: v for k, v in patch.items() if k not in FORBIDDEN_PATCH_KEYS}


# Never accepted as tool input, at any level. These are absent rather than
# gated: a parameter that exists will eventually be reachable.
FORBIDDEN_PATCH_KEYS = frozenset({"share", "image", "base", "iso", "console_password"})
