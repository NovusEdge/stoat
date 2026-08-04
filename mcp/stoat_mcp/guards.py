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
    """A token bucket per tool name.

    The MCP spec makes rate limiting a server MUST. The numbers here are a
    starting point rather than a tuned value: generous enough that ordinary
    agent work never notices, tight enough that a runaway loop calling
    `create` a thousand times stops being the host's problem.

    Not thread-safe on purpose; the stdio server is single-threaded, and a
    lock here would imply a concurrency story that does not exist.
    """

    def __init__(self, capacity: int = 30, refill_per_second: float = 0.5) -> None:
        if capacity < 1:
            raise ValueError("capacity must be at least 1, or nothing can ever run")
        if refill_per_second <= 0:
            # A bucket that never refills is a bucket that permanently bricks
            # the tool after `capacity` calls. Refusing at construction beats
            # discovering it in production, and it is why the message below
            # can divide by this without a guard.
            raise ValueError("refill_per_second must be positive")
        self.capacity = capacity
        self.refill_per_second = refill_per_second
        self._buckets: dict[str, tuple[float, float]] = {}

    def check(self, tool: str, now: float | None = None) -> None:
        """Consume one token for `tool`, raising if the bucket is empty."""
        now = time.monotonic() if now is None else now
        tokens, last = self._buckets.get(tool, (float(self.capacity), now))
        tokens = min(self.capacity, tokens + (now - last) * self.refill_per_second)
        if tokens < 1.0:
            wait = (1.0 - tokens) / self.refill_per_second
            raise GuardRejection(
                f"rate limit reached for {tool}; retry in about {wait:.0f}s"
            )
        self._buckets[tool] = (tokens - 1.0, now)


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
