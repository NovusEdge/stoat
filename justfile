version := `git describe --tags --always 2>/dev/null || echo dev`
ldflags := "-s -w -X main.version=" + version
prefix := env_var_or_default("PREFIX", env_var("HOME") / ".local/bin")
scratch := "/tmp/stoat-scratch"

# show available recipes
default:
    @just --list --unsorted

# build ./stoat
build:
    go build -ldflags "{{ldflags}}" -o stoat ./cmd/stoat

# build and install to $PREFIX (default ~/.local/bin)
install: build
    install -Dm755 stoat {{prefix}}/stoat
    @echo "installed {{prefix}}/stoat ({{version}})"
    @command -v stoat >/dev/null || echo "note: {{prefix}} is not on your PATH"

# remove the installed binary — never touches ~/.stoat
uninstall:
    rm -f {{prefix}}/stoat
    @echo "removed {{prefix}}/stoat — your VMs in ${STOAT_HOME:-~/.stoat} were left alone"

# run against your real ~/.stoat
run *args: build
    ./stoat {{args}}

# run against a throwaway data root, so dev cannot touch your real VMs
dev *args: build
    @mkdir -p {{scratch}}
    @echo "STOAT_HOME={{scratch}}"
    STOAT_HOME={{scratch}} ./stoat {{args}}

# delete the throwaway data root used by `just dev`
dev-reset:
    rm -rf {{scratch}}
    @echo "wiped {{scratch}}"

test:
    go test ./...

# verbose tests for one package, e.g. `just test-pkg apkovl`
test-pkg pkg:
    go test ./internal/{{pkg}}/ -v

# tests with the race detector
race:
    go test -race ./...

# coverage summary per package
cover:
    go test -cover ./...

# exactly what .githooks/pre-commit runs
check:
    gofmt -l .
    go vet ./...
    go build ./...

# format in place
fmt:
    gofmt -w .

tidy:
    go mod tidy
    @grep '^go ' go.mod

# enable the pre-commit and commit-msg hooks
hooks:
    git config core.hooksPath .githooks
    @echo "hooks enabled: $(git config core.hooksPath)"

# what stoat knows about: VMs, ISOs, ports
vms:
    #!/usr/bin/env sh
    root="${STOAT_HOME:-$HOME/.stoat}"
    [ -d "$root" ] || { echo "no data root at $root"; exit 0; }
    for d in "$root"/*/; do
      n=$(basename "$d")
      case "$n" in isos|recipes|logs) continue ;; esac
      if [ -f "$d/vm.toml" ]; then
        port=$(sed -n 's/^sshport *= *//p' "$d/vm.toml")
        mode=$(sed -n 's/^mode *= *//p' "$d/vm.toml" | tr -d '"')
        state=stopped
        [ -f "$d/qemu.pid" ] && kill -0 "$(cat "$d/qemu.pid")" 2>/dev/null && state=running
        printf '%-16s %-6s %-8s ssh:%s\n' "$n" "$mode" "$state" "${port:-?}"
      else
        printf '%-16s %s\n' "$n" "(no vm.toml)"
      fi
    done
    echo "--- isos:"
    ls -1 "$root/isos" 2>/dev/null || echo "  none"

# tail stoat's log
logs n="50":
    @tail -n {{n}} "${STOAT_HOME:-$HOME/.stoat}/logs/stoat.log" 2>/dev/null || echo "no log yet"

# tail a VM's last provision run, e.g. `just provision-log alpine-test`
provision-log name:
    @tail -n 50 "${STOAT_HOME:-$HOME/.stoat}/{{name}}/last-provision.log" 2>/dev/null || echo "no provision log for {{name}}"

# check host prerequisites
doctor:
    #!/usr/bin/env sh
    for b in qemu-system-x86_64 qemu-img ssh ssh-keygen go; do
      if command -v "$b" >/dev/null; then echo "ok    $b"; else echo "MISS  $b"; fi
    done
    if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
      echo "ok    /dev/kvm"
    else
      echo "MISS  /dev/kvm not accessible — sudo usermod -aG kvm \$USER, then re-login"
    fi

# remove build artifacts (never touches your VMs)
clean:
    rm -f stoat
    go clean -testcache

# tag a release; pushing the tag triggers the release workflow
tag v:
    @git diff --quiet || { echo "working tree dirty"; exit 1; }
    git tag {{v}}
    @echo "created {{v}} — push with: git push origin {{v}}"
