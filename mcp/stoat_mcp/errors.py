"""Errors mirroring the JSON contract (docs/reference/json.md).

Code constants exist so tool code branches on a name rather than a string
literal typed twice. They are deliberately NOT an enum: the contract says a
consumer MUST treat an unrecognized code as a generic failure rather than
crashing, and an enum makes the natural spelling of that a ValueError.
"""

from __future__ import annotations

# Every code the contract defines today. A code not in this list is still a
# valid failure; see StoatError.
NOT_FOUND = "not_found"
BROKEN = "broken"
NAME_TAKEN = "name_taken"
INVALID_SPEC = "invalid_spec"
IMAGE_NOT_DOWNLOADED = "image_not_downloaded"
RECIPE_NOT_APPLICABLE = "recipe_not_applicable"
NOT_RUNNING = "not_running"
ALREADY_RUNNING = "already_running"
NO_DISK = "no_disk"
IMMUTABLE_FIELD = "immutable_field"
DISK_SHRINK = "disk_shrink"
CANNOT_REACH = "cannot_reach"
APPLIED_AT_BOOT = "applied_at_boot"
UNKNOWN_LOG = "unknown_log"
TIMEOUT = "timeout"
CANCELED = "canceled"
USAGE = "usage"
CONFIRMATION_REQUIRED = "confirmation_required"
INTERNAL = "internal"


class StoatError(Exception):
    """A stoat command answered with ok:false.

    This is a normal outcome, not a bug: "no such VM" and "that VM is already
    running" both arrive this way. Branch on `code`; `message` is human text
    whose wording is explicitly not part of the contract.
    """

    def __init__(self, code: str, message: str, subject: str = "", kind: str = "") -> None:
        super().__init__(f"{code}: {message}")
        self.code = code
        self.message = message
        # Reserved in the contract and not emitted by any command today.
        # Carried anyway so this class does not need changing when they are.
        self.subject = subject
        self.kind = kind


class StoatCrashed(Exception):
    """The process exited without ever writing a terminal result line.

    The contract's guarantee is "either a result line, or the process died",
    so this is the second case and the only one where the exit code carries
    information. It means a panic, a signal, or a binary that is not stoat.
    """

    def __init__(self, returncode: int, stdout: str = "") -> None:
        super().__init__(f"stoat exited {returncode} without a result line")
        self.returncode = returncode
        self.stdout = stdout


class ContractMismatch(Exception):
    """The stoat binary speaks a different contract version than we expect.

    Raised at startup rather than on first use, so a stale binary fails with
    a message naming both versions instead of a KeyError three tools later.
    """

    def __init__(self, found: int, expected: int) -> None:
        super().__init__(
            f"stoat speaks JSON contract v{found}, this server needs v{expected}. "
            "Upgrade whichever is older."
        )
        self.found = found
        self.expected = expected


class GuardRejection(Exception):
    """A deterministic block refused the call before stoat ever ran.

    Distinct from StoatError on purpose: this never reached the binary, so
    nothing happened and nothing needs undoing.
    """
