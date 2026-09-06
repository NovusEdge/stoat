#!/bin/sh
set -e

user=${STOAT_PARAM_USER:-}
venv_dir=${STOAT_PARAM_VENV_DIR:-}

if [ -z "$user" ] || ! id -u "$user" >/dev/null 2>&1; then
    printf 'python-dev: user "%s": user does not exist\n' "$user" >&2
    exit 1
fi

stoat_pkg_setup
stoat_pkg_install python python-pip

as_user() {
    if command -v runuser >/dev/null 2>&1; then
        runuser -u "$user" -- "$@"
    elif command -v sudo >/dev/null 2>&1; then
        sudo -n -u "$user" -- "$@"
    else
        command=$1
        shift
        su -s /bin/sh "$user" -c 'exec "$@"' stoat "$command" "$@"
    fi
}

python_bin=$(command -v python3 2>/dev/null || command -v python 2>/dev/null || true)
[ -n "$python_bin" ] || { echo "python-dev: python3 was not installed" >&2; exit 1; }
pip_path=$(command -v pip3 2>/dev/null || command -v pip 2>/dev/null || true)
[ -n "$pip_path" ] || { echo "python-dev: pip was not installed" >&2; exit 1; }
python_version=$($python_bin --version 2>&1)

venv_python=
tmp_root=
cleanup() {
    if [ -n "$tmp_root" ] && [ -d "$tmp_root" ]; then rm -rf "$tmp_root"; fi
}
on_signal() { cleanup; exit 1; }
trap cleanup EXIT
trap on_signal HUP INT TERM

if [ -z "$venv_dir" ]; then
    tmp_root=$(mktemp -d "${TMPDIR:-/tmp}/stoat-python-dev.XXXXXX")
    chmod 755 "$tmp_root"
    venv_dir="$tmp_root/venv"
    as_user "$python_bin" -m venv "$venv_dir"
    venv_python="$venv_dir/bin/python"
else
    case "$venv_dir" in
        /*) ;;
        *) printf 'python-dev: venv_dir "%s": path must be absolute\n' "$venv_dir" >&2; exit 1 ;;
    esac
    if [ -e "$venv_dir" ] || [ -L "$venv_dir" ]; then
        if [ ! -d "$venv_dir" ] || [ ! -f "$venv_dir/pyvenv.cfg" ]; then
            printf 'python-dev: venv_dir "%s": existing path is not a Python virtual environment\n' "$venv_dir" >&2; exit 1
        fi
        venv_python="$venv_dir/bin/python"
        if [ ! -x "$venv_python" ] ||
            ! as_user "$venv_python" -c 'import sys; raise SystemExit(sys.prefix == sys.base_prefix)' ||
            ! as_user "$venv_python" -m pip --version >/dev/null 2>&1
        then
            printf 'python-dev: venv_dir "%s": existing path is not a compatible Python virtual environment\n' "$venv_dir" >&2; exit 1
        fi
        owner_uid=$(stat -c '%u' "$venv_dir")
        user_uid=$(id -u "$user")
        if [ "$owner_uid" != "$user_uid" ]; then
            printf 'python-dev: venv_dir "%s": existing environment is owned by uid %s, want uid %s\n' "$venv_dir" "$owner_uid" "$user_uid" >&2; exit 1
        fi
    else
        parent=$(dirname "$venv_dir")
        as_user mkdir -p "$parent"
        as_user "$python_bin" -m venv "$venv_dir"
        venv_python="$venv_dir/bin/python"
    fi
fi

as_user "$venv_python" -c 'import sys; raise SystemExit(sys.prefix == sys.base_prefix)'
as_user "$venv_python" -m pip --version >/dev/null

if [ -n "${STOAT_OUTPUT:-}" ]; then
    {
        printf 'python=%s\n' "$python_bin"
        printf 'python_version=%s\n' "$python_version"
        printf 'pip=%s\n' "$pip_path"
    } >> "$STOAT_OUTPUT"
    if [ -n "$tmp_root" ]; then printf 'venv=\nvenv_python=\n' >> "$STOAT_OUTPUT"; else printf 'venv=%s\nvenv_python=%s\n' "$venv_dir" "$venv_python" >> "$STOAT_OUTPUT"; fi
fi

root_fstype=$(awk '$2 == "/" { print $3 }' /proc/mounts)
case "$root_fstype" in
tmpfs | overlay)
    echo "NOTE: this is a live VM (root is $root_fstype, in RAM). Everything installed above is gone after a reboot. Rebooting will NOT bring it back. Use a disk VM to keep it."
    ;;
*)
    echo "installed on a disk VM (root is $root_fstype), so this survives a reboot."
    ;;
esac
