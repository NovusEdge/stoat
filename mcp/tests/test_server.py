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

from stoat_mcp import server

# The class table from docs/design/mcp-server.md §5.
EXPECTED_ANNOTATIONS = {
    "list_vms": {"readOnlyHint": True, "destructiveHint": False},
    "vm_status": {"readOnlyHint": True, "destructiveHint": False},
    "list_images": {"readOnlyHint": True, "destructiveHint": False},
    "list_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "check_recipes": {"readOnlyHint": True, "destructiveHint": False},
    "logs": {"readOnlyHint": True, "destructiveHint": False},
    "doctor": {"readOnlyHint": True, "destructiveHint": False},
    "create": {"readOnlyHint": False, "destructiveHint": False},
    "start": {"readOnlyHint": False, "destructiveHint": False},
    "stop": {"readOnlyHint": False, "destructiveHint": False},
    "apply_recipes": {"readOnlyHint": False, "destructiveHint": False},
    "update": {"readOnlyHint": False, "destructiveHint": False},
    "clone": {"readOnlyHint": False, "destructiveHint": False},
    "snapshot": {"readOnlyHint": False, "destructiveHint": False},
    "restore": {"readOnlyHint": False, "destructiveHint": False},
    "forward": {"readOnlyHint": False, "destructiveHint": False},
    "wait": {"readOnlyHint": False, "destructiveHint": False},
    "destroy": {"readOnlyHint": False, "destructiveHint": True},
    "prune": {"readOnlyHint": False, "destructiveHint": True},
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
