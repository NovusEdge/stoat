"""Adversarial tests for guards.py, the only file that stands between an
agent's arguments and a subprocess running on the host.

Every test here builds its own tmp_path "data root" and never touches
~/.stoat. check_host_path's whole job is confining a path to
<data_root>/shared/<vm>/, so most of these are attempts to escape that
confinement: symlinks, .., a sibling directory that shares a string prefix,
absolute paths elsewhere, and the degenerate inputs (empty, whitespace,
null byte, "." / "..").
"""

from __future__ import annotations

import os

import pytest

from stoat_mcp.errors import GuardRejection
from stoat_mcp.guards import (
    RateLimiter,
    check_host_path,
    check_image_id,
    check_vm_name,
    shared_dir,
    strip_forbidden,
)

# ---------------------------------------------------------------------------
# check_vm_name
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("name", ["work", "work-2", "work.2", "a", "A1", "9x"])
def test_vm_name_accepts_ordinary_names(name: str) -> None:
    assert check_vm_name(name) == name


@pytest.mark.parametrize(
    "name",
    [
        "",
        "   ",
        " work",
        "work ",
        ".",
        "..",
        "../etc",
        "../../etc/passwd",
        "a/b",
        "a\\b",
        ".hidden",
        "-leading-dash",
        "work\x00",
        "work;rm -rf /",
        "work\n",
    ],
)
def test_vm_name_rejects_bad_input(name: str) -> None:
    with pytest.raises(GuardRejection):
        check_vm_name(name)


def test_vm_name_rejects_non_string() -> None:
    with pytest.raises(GuardRejection):
        check_vm_name(None)  # type: ignore[arg-type]


def test_vm_name_rejects_absolute_path() -> None:
    with pytest.raises(GuardRejection):
        check_vm_name("/etc/passwd")


def test_vm_name_rejects_unicode_lookalike_separator() -> None:
    # U+2215 DIVISION SLASH looks like a path separator but is not one; it
    # is not a separator os.sep recognizes, so it is caught (if at all) by
    # the character-class regex, not the separator check. Either accepting
    # or rejecting it is defensible, but it must never be treated as a real
    # path separator that changes shared_dir's resolution.
    name = "work∕etc"
    with pytest.raises(GuardRejection):
        check_vm_name(name)


# ---------------------------------------------------------------------------
# check_image_id
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("image", ["alpine-virt", "ubuntu-24.04", "debian_13", "a"])
def test_image_id_accepts_catalog_ids(image: str) -> None:
    assert check_image_id(image) == image


@pytest.mark.parametrize(
    "image",
    [
        "",
        "   ",
        "/abs/path.qcow2",
        "../escape.qcow2",
        "~/image.qcow2",
        "relative/path.qcow2",
        "a\\b",
    ],
)
def test_image_id_rejects_paths(image: str) -> None:
    with pytest.raises(GuardRejection):
        check_image_id(image)


def test_image_id_rejects_non_string() -> None:
    with pytest.raises(GuardRejection):
        check_image_id(123)  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# check_host_path
# ---------------------------------------------------------------------------


def test_host_path_accepts_file_inside_sandbox(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    f = sandbox / "file.txt"
    f.write_text("hi")
    resolved = check_host_path(str(f), "work", tmp_path)
    assert resolved == str(f.resolve())


def test_host_path_accepts_the_sandbox_root_itself(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    assert check_host_path(str(sandbox), "work", tmp_path) == str(sandbox)


def test_host_path_rejects_relative_path(tmp_path):
    with pytest.raises(GuardRejection):
        check_host_path("relative/file.txt", "work", tmp_path)


def test_host_path_rejects_traversal_out_of_sandbox(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    escaped = str(sandbox / ".." / ".." / "etc" / "passwd")
    with pytest.raises(GuardRejection):
        check_host_path(escaped, "work", tmp_path)


def test_host_path_rejects_absolute_path_entirely_outside(tmp_path):
    outside = tmp_path / "elsewhere" / "secret.txt"
    outside.parent.mkdir(parents=True)
    outside.write_text("nope")
    with pytest.raises(GuardRejection):
        check_host_path(str(outside), "work", tmp_path)


def test_host_path_rejects_sibling_sharing_a_string_prefix(tmp_path):
    """The named example from the design doc: shared/work-evil is a STRING
    prefix match against shared/work but not a directory-parent match, and
    the guard must compare by path parts, not by string prefix."""
    shared_root = tmp_path / "shared"
    shared_root.mkdir()
    work = shared_root / "work"
    work.mkdir()
    evil = shared_root / "work-evil"
    evil.mkdir()
    evil_file = evil / "secret.txt"
    evil_file.write_text("nope")

    with pytest.raises(GuardRejection):
        check_host_path(str(evil_file), "work", tmp_path)


def test_host_path_rejects_symlink_escape(tmp_path):
    """A symlink INSIDE the sandbox pointing outside it. This is the
    scenario check_host_path's docstring calls out explicitly: the guest has
    the sandbox mounted read-write and can create such a symlink itself, so
    resolution must happen before the confinement check, not after."""
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    outside = tmp_path / "outside"
    outside.mkdir()
    secret = outside / "secret.txt"
    secret.write_text("nope")

    link = sandbox / "escape"
    link.symlink_to(outside)

    with pytest.raises(GuardRejection):
        check_host_path(str(link / "secret.txt"), "work", tmp_path)


def test_host_path_rejects_symlink_pointing_directly_out(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    outside = tmp_path / "outside" / "secret.txt"
    outside.parent.mkdir(parents=True)
    outside.write_text("nope")

    link = sandbox / "link.txt"
    link.symlink_to(outside)

    with pytest.raises(GuardRejection):
        check_host_path(str(link), "work", tmp_path)


def test_host_path_accepts_symlink_that_stays_inside_sandbox(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    real = sandbox / "real.txt"
    real.write_text("hi")
    link = sandbox / "link.txt"
    link.symlink_to(real)

    resolved = check_host_path(str(link), "work", tmp_path)
    assert resolved == str(real.resolve())


def test_host_path_rejects_empty_and_whitespace(tmp_path):
    for bad in ("", "   ", "\t"):
        with pytest.raises(GuardRejection):
            check_host_path(bad, "work", tmp_path)


def test_host_path_rejects_null_byte(tmp_path):
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    with pytest.raises(GuardRejection):
        check_host_path(str(sandbox / "a\x00b"), "work", tmp_path)


def test_host_path_expands_tilde_to_the_callers_home_not_ours(tmp_path, monkeypatch):
    # "~" must expand via the OS's normal rule (the invoking user's home),
    # not be treated as a literal directory name; os.path.realpath would
    # otherwise resolve a literal "~" relative to the cwd, which is wrong in
    # a different way.
    monkeypatch.setenv("HOME", str(tmp_path))
    sandbox = shared_dir("work", tmp_path)
    sandbox.mkdir(parents=True)
    f = sandbox / "f.txt"
    f.write_text("hi")
    home_relative = os.path.join("~", os.path.relpath(f, tmp_path))
    resolved = check_host_path(home_relative, "work", tmp_path)
    assert resolved == str(f.resolve())


def test_host_path_different_vm_names_have_disjoint_sandboxes(tmp_path):
    sandbox_a = shared_dir("work", tmp_path)
    sandbox_a.mkdir(parents=True)
    f = sandbox_a / "f.txt"
    f.write_text("hi")
    # Same file, but authorised for a different VM: must be rejected.
    with pytest.raises(GuardRejection):
        check_host_path(str(f), "other", tmp_path)


def test_host_path_rejects_invalid_vm_name_even_as_a_path_component(tmp_path):
    # check_host_path calls shared_dir -> check_vm_name internally, so a
    # malicious "vm" argument (not the path) is caught the same way.
    with pytest.raises(GuardRejection):
        check_host_path(str(tmp_path / "shared" / "work" / "f.txt"), "../escape", tmp_path)


# ---------------------------------------------------------------------------
# strip_forbidden
# ---------------------------------------------------------------------------


def test_strip_forbidden_removes_every_forbidden_key():
    patch = {
        "ram": 4096,
        "share": "/home/user/whatever",
        "image": "/abs/evil.qcow2",
        "base": "/abs/base.qcow2",
        "iso": "/abs/thing.iso",
        "console_password": "hunter2",
    }
    cleaned = strip_forbidden(patch)
    assert cleaned == {"ram": 4096}


def test_strip_forbidden_is_a_no_op_on_a_clean_patch():
    patch = {"ram": 4096, "cpus": 2}
    assert strip_forbidden(patch) == patch


def test_strip_forbidden_does_not_mutate_the_input():
    patch = {"share": "x", "ram": 1}
    strip_forbidden(patch)
    assert patch == {"share": "x", "ram": 1}


# ---------------------------------------------------------------------------
# RateLimiter
# ---------------------------------------------------------------------------


def test_rate_limiter_allows_up_to_capacity():
    # A tiny nonzero refill rate, not 0.0: RateLimiter.check divides by
    # refill_per_second when it needs to report a wait time, so an actual
    # 0.0 raises ZeroDivisionError instead of GuardRejection once the bucket
    # is empty. See the report for this finding; a still-positive rate this
    # small is effectively "never refills" within any of these tests.
    rl = RateLimiter(capacity=3, refill_per_second=1e-9)
    rl.check("create", now=0.0)
    rl.check("create", now=0.0)
    rl.check("create", now=0.0)
    with pytest.raises(GuardRejection):
        rl.check("create", now=0.0)


def test_rate_limiter_buckets_are_independent_per_tool():
    rl = RateLimiter(capacity=1, refill_per_second=1e-9)
    rl.check("create", now=0.0)
    # A different tool has its own bucket, unaffected by "create" being spent.
    rl.check("destroy", now=0.0)


def test_rate_limiter_refills_over_time():
    rl = RateLimiter(capacity=1, refill_per_second=1.0)
    rl.check("create", now=0.0)
    with pytest.raises(GuardRejection):
        rl.check("create", now=0.1)
    # A full second later, one token has refilled.
    rl.check("create", now=1.0)
