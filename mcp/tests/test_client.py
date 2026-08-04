"""Client.run against a FAKE stoat binary: a small Python script that echoes
fixed JSON Lines, never the real CLI. This exercises envelope handling
(docs/reference/json.md) in isolation, without spinning up any VM.

Each fake is a tiny script written to tmp_path, made executable, and
selected by the test's own argv (the fakes below dispatch on sys.argv[1]
after --json, the same position a real subcommand name would occupy).
"""

from __future__ import annotations

import os
import stat
import sys
import textwrap

import pytest

from stoat_mcp.client import Client
from stoat_mcp.errors import StoatCrashed, StoatError

FAKE = textwrap.dedent(
    """\
    #!/usr/bin/env python3
    import sys

    # args: --json <subcommand>. The subcommand picks which canned behaviour
    # to emit, exactly like a real fixture-driven fake would.
    args = sys.argv[1:]
    cmd = args[1] if len(args) > 1 else args[0]

    def emit(line):
        print(line, flush=True)

    if cmd == "ok":
        emit('{"v":1,"type":"result","cmd":"ok","ok":true,"data":{"answer":42}}')
    elif cmd == "err":
        emit('{"v":1,"type":"result","cmd":"err","ok":false,"error":{"code":"not_found","message":"no such VM"}}')
        sys.exit(1)
    elif cmd == "crash":
        # Exits nonzero without ever writing a result line.
        sys.stderr.write("panic: boom\\n")
        sys.exit(1)
    elif cmd == "crash-zero":
        # No result line, but exit 0: still a crash per the contract ("no
        # result line" is what matters, not the exit code by itself).
        sys.exit(0)
    elif cmd == "unknown-event":
        emit('{"v":1,"type":"heartbeat","cmd":"unknown-event","data":{"x":1}}')
        emit('{"v":1,"type":"result","cmd":"unknown-event","ok":true,"data":{}}')
    elif cmd == "bad-json":
        emit("not json at all")
        emit('{"v":1,"type":"result","cmd":"bad-json","ok":true,"data":{"survived":true}}')
    elif cmd == "events":
        emit('{"v":1,"type":"progress","cmd":"events","data":{"percent":50}}')
        emit('{"v":1,"type":"stage","cmd":"events","data":{"recipe":"xfce"}}')
        emit('{"v":1,"type":"log","cmd":"events","data":{"line":"hello"}}')
        emit('{"v":1,"type":"result","cmd":"events","ok":true,"data":{}}')
    elif cmd == "duplicate-result":
        # The contract guarantees exactly one; Client keeps the LAST.
        emit('{"v":1,"type":"result","cmd":"duplicate-result","ok":true,"data":{"which":"first"}}')
        emit('{"v":1,"type":"result","cmd":"duplicate-result","ok":true,"data":{"which":"second"}}')
    elif cmd == "empty-line":
        emit("")
        emit('{"v":1,"type":"result","cmd":"empty-line","ok":true,"data":{}}')
    elif cmd == "no-data":
        emit('{"v":1,"type":"result","cmd":"no-data","ok":true}')
    else:
        sys.stderr.write("unknown fake command\\n")
        sys.exit(2)
    """
)


@pytest.fixture()
def fake_binary(tmp_path):
    path = tmp_path / "fake-stoat"
    path.write_text(FAKE)
    path.chmod(path.stat().st_mode | stat.S_IEXEC | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return str(path)


@pytest.fixture()
def client(fake_binary):
    return Client(binary=fake_binary, default_timeout=10.0)


def test_normal_result(client):
    data = client.run("ok")
    assert data == {"answer": 42}


def test_ok_false_raises_stoat_error_with_code(client):
    with pytest.raises(StoatError) as exc_info:
        client.run("err")
    assert exc_info.value.code == "not_found"
    assert exc_info.value.message == "no such VM"


def test_no_result_line_and_nonzero_exit_raises_stoat_crashed(client):
    with pytest.raises(StoatCrashed) as exc_info:
        client.run("crash")
    assert exc_info.value.returncode == 1


def test_no_result_line_even_with_exit_zero_raises_stoat_crashed(client):
    # "no result line" is the trigger, not the exit code; a fake that exits
    # cleanly but never answers is still a crash per the contract.
    with pytest.raises(StoatCrashed):
        client.run("crash-zero")


def test_unknown_event_type_is_skipped_not_raised(client):
    data = client.run("unknown-event")
    assert data == {}


def test_invalid_json_mid_stream_is_skipped(client):
    data = client.run("bad-json")
    assert data == {"survived": True}


def test_progress_stage_log_events_reach_the_callback(client):
    seen: list[tuple[str, dict]] = []
    data = client.run("events", on_event=lambda kind, payload: seen.append((kind, payload)))
    assert data == {}
    assert seen == [
        ("progress", {"percent": 50}),
        ("stage", {"recipe": "xfce"}),
        ("log", {"line": "hello"}),
    ]


def test_duplicate_result_lines_keep_the_last(client):
    data = client.run("duplicate-result")
    assert data == {"which": "second"}


def test_blank_lines_are_skipped(client):
    data = client.run("empty-line")
    assert data == {}


def test_missing_data_field_is_an_empty_dict(client):
    data = client.run("no-data")
    assert data == {}


def test_check_contract_raises_on_mismatch(tmp_path):
    path = tmp_path / "fake-stoat"
    path.write_text(
        textwrap.dedent(
            """\
            #!/usr/bin/env python3
            print('{"v":1,"type":"result","cmd":"version","ok":true,"data":{"contract":99,"version":"x"}}')
            """
        )
    )
    path.chmod(path.stat().st_mode | stat.S_IEXEC)
    from stoat_mcp.errors import ContractMismatch

    c = Client(binary=str(path), default_timeout=10.0)
    with pytest.raises(ContractMismatch) as exc_info:
        c.check_contract()
    assert exc_info.value.found == 99
