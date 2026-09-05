"""Structural tests for the tool surface itself: every tool sets
additionalProperties: false, no forbidden surface (docs/design/mcp-
server.md §4) is registered at all, annotations match the spec's table
(§5), and no tool accepts a parameter literally named `share`.

These do not invoke stoat and do not need a binary: they only ask fastmcp
what it registered.
"""

from __future__ import annotations

import asyncio

import pytest
from fastmcp.exceptions import ToolError

from stoat_mcp import server

# The class table from docs/design/mcp-server.md §5.
EXPECTED_ANNOTATIONS = {
    "list_vms": {"readOnlyHint": True, "destructiveHint": False},
    "vm_status": {"readOnlyHint": True, "destructiveHint": False},
    "list_images": {"readOnlyHint": True, "destructiveHint": False},
    "list_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "check_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "logs": {"readOnlyHint": True, "destructiveHint": False},
    "search_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "doctor": {"readOnlyHint": True, "destructiveHint": False},
    "plan_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "create": {"readOnlyHint": False, "destructiveHint": False},
    "start": {"readOnlyHint": False, "destructiveHint": False},
    "stop": {"readOnlyHint": False, "destructiveHint": False},
    "update": {"readOnlyHint": False, "destructiveHint": False},
    "add_recipe": {"readOnlyHint": False, "destructiveHint": False},
    "update_recipe": {"readOnlyHint": False, "destructiveHint": False},
    "clone": {"readOnlyHint": False, "destructiveHint": False},
    "snapshot": {"readOnlyHint": False, "destructiveHint": False},
    "forward": {"readOnlyHint": False, "destructiveHint": False},
    "wait": {"readOnlyHint": False, "destructiveHint": False},
    "destroy": {"readOnlyHint": False, "destructiveHint": True},
    "prune": {"readOnlyHint": False, "destructiveHint": True},
    "remove_recipe": {"readOnlyHint": False, "destructiveHint": True},
    "restore": {"readOnlyHint": False, "destructiveHint": True},
    # A recipe body is arbitrary guest code, so apply_recipes carries exec's
    # hints and exec's allow_exec check.
    "apply_recipes": {
        "readOnlyHint": False,
        "destructiveHint": True,
        "openWorldHint": True,
    },
    "exec": {"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
    "copy_to": {"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
    "copy_from": {"readOnlyHint": False, "destructiveHint": True, "openWorldHint": True},
}

# §4: never exposed as tools at all, absence rather than a gate.
FORBIDDEN_TOOL_NAMES = {"recipe_new", "ssh_command", "ssh-command", "pull"}


def _tools() -> dict[str, object]:
    async def _get():
        return await server.mcp.list_tools()

    return {t.name: t for t in asyncio.run(_get())}


def test_every_expected_tool_is_registered():
    tools = _tools()
    missing = set(EXPECTED_ANNOTATIONS) - set(tools)
    assert not missing, f"tools not registered: {missing}"


def test_no_unexpected_tools_are_registered():
    tools = _tools()
    extra = set(tools) - set(EXPECTED_ANNOTATIONS)
    assert not extra, f"unexpected tools registered: {extra}"


def test_forbidden_surfaces_are_not_registered_at_all():
    tools = _tools()
    for name in FORBIDDEN_TOOL_NAMES:
        assert name not in tools


@pytest.mark.parametrize("name", sorted(EXPECTED_ANNOTATIONS))
def test_tool_schema_sets_additional_properties_false(name):
    tool = _tools()[name]
    assert tool.parameters.get("additionalProperties") is False, (
        f"{name} does not reject unexpected parameters"
    )


@pytest.mark.parametrize("name", sorted(EXPECTED_ANNOTATIONS))
def test_annotations_match_the_spec_table(name):
    tool = _tools()[name]
    expected = EXPECTED_ANNOTATIONS[name]
    actual = tool.annotations
    assert actual is not None, f"{name} has no annotations"
    for key, value in expected.items():
        assert getattr(actual, key) == value, f"{name}.{key}"
    # Hints not in the table must not be silently set true either.
    for key in ("readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"):
        if key not in expected:
            assert getattr(actual, key) in (None, False), f"{name}.{key} unexpectedly set"


def test_no_tool_has_a_parameter_named_share():
    tools = _tools()
    for name, tool in tools.items():
        props = tool.parameters.get("properties", {})
        assert "share" not in props, f"{name} accepts a share parameter"


def test_no_tool_has_a_parameter_named_console_password_or_image_path_flags():
    tools = _tools()
    for name, tool in tools.items():
        props = tool.parameters.get("properties", {})
        assert "console_password" not in props, name


def test_create_only_accepts_a_catalog_image_id_field_named_image():
    tools = _tools()
    props = tools["create"].parameters.get("properties", {})
    assert "image" in props
    assert "share" not in props
    assert "console_password" not in props


def test_every_tool_has_a_non_empty_human_readable_description():
    tools = _tools()
    for name, tool in tools.items():
        assert tool.description and len(tool.description.strip()) > 20, name


def test_no_tool_description_contains_an_em_dash():
    tools = _tools()
    for name, tool in tools.items():
        assert "—" not in (tool.description or ""), name


class _FakeClient:
    """Records the argv each tool builds and answers from a fixed dict."""

    def __init__(self, answers: dict[str, object] | None = None) -> None:
        self.calls: list[tuple[str, ...]] = []
        self.answers = answers or {}

    def run(self, *args: str, **_: object) -> dict[str, object]:
        self.calls.append(args)
        return dict(self.answers.get(args[0], {}))


@pytest.fixture
def fake_client(monkeypatch):
    client = _FakeClient({"get": {"vm": {"allow_exec": True}}})
    monkeypatch.setattr(server, "get_client", lambda: client)
    return client


def test_apply_recipes_refuses_a_vm_with_allow_exec_false(monkeypatch):
    # A recipe body is arbitrary guest code. allow_exec=false blocked exec
    # and copy while apply ran scripts as root, which made the opt-out a
    # partial one.
    client = _FakeClient({"get": {"vm": {"allow_exec": False}}})
    monkeypatch.setattr(server, "get_client", lambda: client)
    with pytest.raises(ToolError):
        server.apply_recipes("work")
    assert all(c[0] != "apply" for c in client.calls)


def test_plan_recipes_runs_a_dry_run_and_needs_no_exec_permission(monkeypatch):
    client = _FakeClient({"get": {"vm": {"allow_exec": False}}})
    monkeypatch.setattr(server, "get_client", lambda: client)
    server.plan_recipes("work")
    assert client.calls == [("apply", "work", "--dry-run")]


def test_wait_clamps_the_timeout(fake_client):
    server.wait("work", timeout_seconds=10**6)
    argv = fake_client.calls[-1]
    assert argv[argv.index("--timeout") + 1] == f"{server.MAX_WAIT_SECONDS}s"


def test_logs_clamps_the_line_count(fake_client):
    server.logs("work", n=10**6)
    argv = fake_client.calls[-1]
    assert argv[argv.index("-n") + 1] == str(server.MAX_LOG_LINES)


def test_forward_refuses_a_pair_that_kong_reads_as_a_flag(fake_client):
    with pytest.raises(ToolError):
        server.forward("work", pairs=["--clear"])
    assert fake_client.calls == []


@pytest.mark.parametrize("term", ["-tail", "--refresh", "--"])
def test_search_recipes_preserves_a_leading_dash_as_data(fake_client, term):
    server.search_recipes(term)
    assert fake_client.calls == [("recipe", "search", "--", term)]


def test_add_recipe_accepts_a_slash_containing_ref_and_uses_variadic_argv(fake_client):
    server.add_recipe("tailscale", ref="feature/topic")
    assert fake_client.calls == [("recipe", "add", "tailscale@feature/topic", "-y")]


@pytest.mark.parametrize(
    "call",
    [
        lambda: server.add_recipe("https://github.com/x/stoat-tailscale"),
        lambda: server.add_recipe("tailscale@../escape"),
        lambda: server.add_recipe("tailscale@feature..topic"),
        lambda: server.add_recipe("tailscale@feature/.hidden"),
        lambda: server.add_recipe("tailscale@feature/topic.lock"),
        lambda: server.add_recipe("-y"),
        lambda: server.update_recipe("tailscale@v1.2"),
        lambda: server.remove_recipe("../tailscale"),
        lambda: server.remove_recipe("-y"),
    ],
)
def test_recipe_tools_refuse_unsafe_names_before_cli(call, fake_client):
    with pytest.raises(ToolError):
        call()
    assert fake_client.calls == []


def test_update_and_remove_recipe_use_plain_names_and_remove_has_no_force(fake_client):
    server.update_recipe("tailscale")
    server.update_recipe()
    server.remove_recipe("tailscale")
    assert fake_client.calls == [
        ("recipe", "update", "tailscale"),
        ("recipe", "update"),
        ("recipe", "rm", "tailscale", "-y"),
    ]
