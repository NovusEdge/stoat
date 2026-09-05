"""The only place that runs the stoat binary.

Everything here is dictated by docs/reference/json.md rather than chosen. The
four rules that matter, and why each is not a preference:

1. stdout only, stderr to DEVNULL. Errors arrive in the result envelope on
   stdout. A consumer that merges two pipes to reconstruct one result
   eventually interleaves them wrong, and reading two pipes sequentially
   deadlocks when either buffer fills.
2. Exactly one line has type "result" and it is last. Read to EOF, keep it.
3. An unrecognized event type is SKIPPED, never an error. That is what makes
   adding a new event type a non-breaking change.
4. The exit code is consulted for exactly one thing: whether a result line
   arrived at all. Never branch on it otherwise; exec deliberately exits 0
   while reporting a nonzero guest status in the payload.
"""

from __future__ import annotations

import json
import os
import selectors
import signal
import shutil
import subprocess
import time
from collections.abc import Callable, Iterator, Sequence
from typing import Any

from .errors import ContractMismatch, StoatCrashed, StoatError

# Bumped only when json.md's "v" bumps, which happens only for a removal or a
# meaning change. Additions never bump it, so this does not track features.
#
# v2: the recipe system moved to directories with a recipe.toml manifest, and
# Recipe lost label, target_os and shared. There is no v1 path on either side;
# a server built for v1 refuses to start against a v2 binary and vice versa,
# which is the entire point of checking this at startup.
EXPECTED_CONTRACT = 3

# Non-terminal event types we understand. Anything else is skipped per rule 3.
EVENT_PROGRESS = "progress"
EVENT_STAGE = "stage"
EVENT_LOG = "log"

# Called with (event_type, data) for each non-terminal line.
EventHandler = Callable[[str, dict[str, Any]], None]


def find_binary() -> str:
    """Resolve the stoat binary: $STOAT_BIN, else PATH.

    Fails here rather than at first tool call, so a misconfigured server is a
    startup error with an actionable message.
    """
    override = os.environ.get("STOAT_BIN")
    if override:
        if not os.path.isfile(override) or not os.access(override, os.X_OK):
            raise FileNotFoundError(f"STOAT_BIN={override} is not an executable file")
        return override
    found = shutil.which("stoat")
    if not found:
        raise FileNotFoundError(
            "stoat not found on PATH and STOAT_BIN is unset. "
            "Install stoat or point STOAT_BIN at the binary."
        )
    return found


class Client:
    """Runs stoat subcommands and decodes the JSON Lines envelope."""

    def __init__(self, binary: str | None = None, default_timeout: float | None = 120.0) -> None:
        self.binary = binary or find_binary()
        self.default_timeout = default_timeout

    def check_contract(self) -> int:
        """Verify the binary speaks the contract version we were written for.

        Called once at startup. One subprocess, and it turns "confusing
        KeyError three tools later" into "refused to start, here is why".
        """
        data = self.run("version")
        found = data.get("contract")
        if found != EXPECTED_CONTRACT:
            raise ContractMismatch(found if isinstance(found, int) else -1, EXPECTED_CONTRACT)
        return found

    def run(
        self,
        *args: str,
        timeout: float | None = None,
        on_event: EventHandler | None = None,
    ) -> dict[str, Any]:
        """Run one stoat subcommand and return its result `data`.

        Raises StoatError when the command answered ok:false, and StoatCrashed
        when no result line arrived at all.
        """
        result: dict[str, Any] | None = None
        stdout_lines: list[str] = []

        proc = subprocess.Popen(
            [self.binary, "--json", *args],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            stdin=subprocess.DEVNULL,
            text=False,
            bufsize=0,
            start_new_session=(os.name == "posix"),
        )
        effective_timeout = timeout if timeout is not None else self.default_timeout
        deadline = None if effective_timeout is None else time.monotonic() + effective_timeout
        selector: selectors.BaseSelector | None = None
        returncode: int | None = None
        try:
            assert proc.stdout is not None
            selector = selectors.DefaultSelector()
            selector.register(proc.stdout, selectors.EVENT_READ)
            buffer = bytearray()

            def consume(line: bytes) -> None:
                nonlocal result
                decoded = line.decode("utf-8", errors="replace")
                stdout_lines.append(decoded)
                obj = _decode(decoded)
                if obj is None:
                    return
                kind = obj.get("type")
                if kind == "result":
                    # Keep the LAST one. The contract guarantees exactly one,
                    # but overwriting rather than breaking means a future
                    # violation degrades to "used the final answer" instead of
                    # "silently used a stale one".
                    result = obj
                    return
                if on_event is not None and kind in (EVENT_PROGRESS, EVENT_STAGE, EVENT_LOG):
                    on_event(kind, obj.get("data") or {})
                # Any other type is skipped: rule 3.

            while True:
                remaining = None if deadline is None else deadline - time.monotonic()
                if remaining is not None and remaining <= 0:
                    raise subprocess.TimeoutExpired(proc.args, effective_timeout)
                if not selector.select(remaining):
                    raise subprocess.TimeoutExpired(proc.args, effective_timeout)
                chunk = os.read(proc.stdout.fileno(), 4096)
                if not chunk:
                    if buffer:
                        consume(bytes(buffer))
                    break
                buffer.extend(chunk)
                while True:
                    try:
                        end = buffer.index(10)
                    except ValueError:
                        break
                    consume(bytes(buffer[: end + 1]))
                    del buffer[: end + 1]

            remaining = None if deadline is None else max(0.0, deadline - time.monotonic())
            returncode = proc.wait(timeout=remaining)
        except subprocess.TimeoutExpired:
            _terminate_owned_process_group(proc)
            raise
        finally:
            if proc.poll() is None:
                _terminate_owned_process_group(proc)
            if selector is not None:
                selector.close()
            if proc.stdout is not None:
                proc.stdout.close()

        if result is None:
            raise StoatCrashed(returncode, "".join(stdout_lines))

        if not result.get("ok"):
            err = result.get("error") or {}
            raise StoatError(
                code=str(err.get("code", "internal")),
                message=str(err.get("message", "")),
                subject=str(err.get("subject", "")),
                kind=str(err.get("kind", "")),
            )
        # data is absent for a command whose answer is "it worked" with no
        # payload; an empty dict is the honest reading, not an error.
        return result.get("data") or {}

    def stream(self, *args: str, timeout: float | None = None) -> Iterator[tuple[str, dict[str, Any]]]:
        """Run a command, yielding every event including the terminal result.

        For callers that want to react to progress as it happens rather than
        take a callback. The final yielded pair is ("result", data); errors
        still raise, so a consumer that iterates to exhaustion cannot miss one.
        """
        events: list[tuple[str, dict[str, Any]]] = []
        data = self.run(
            *args,
            timeout=timeout,
            on_event=lambda kind, payload: events.append((kind, payload)),
        )
        yield from events
        yield ("result", data)


def _terminate_owned_process_group(proc: subprocess.Popen[bytes]) -> None:
    """Terminate this invocation and descendants without touching other jobs."""
    if os.name == "posix":
        try:
            os.killpg(proc.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
    elif proc.poll() is None:
        proc.terminate()
    try:
        proc.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        pass
    if os.name == "posix":
        try:
            # The leader can exit after TERM while a descendant ignores it.
            # Escalate the owned group even when proc.wait already returned.
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    elif proc.poll() is None:
        proc.kill()
    proc.wait()


def _decode(line: str) -> dict[str, Any] | None:
    """Parse one stdout line, tolerating anything that is not a JSON object.

    The contract says every stdout line is a JSON object, so a line that is
    not one means either a stoat bug or a wrapper script polluting stdout.
    Skipping is right either way: the result line is what matters, and dying
    on someone's stray `echo` would be a worse failure than ignoring it.
    """
    line = line.strip()
    if not line:
        return None
    try:
        obj = json.loads(line)
    except json.JSONDecodeError:
        return None
    return obj if isinstance(obj, dict) else None


def argv_for_bool(flag: str, value: bool) -> Sequence[str]:
    """Render a kong bool flag. `--flag=false` is the only spelling that unsets.

    Kong treats a bare `--flag` as true; there is no `--no-flag`. Writing this
    once stops each tool from guessing.
    """
    return [f"--{flag}={'true' if value else 'false'}"]
