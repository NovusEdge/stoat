version := `git describe --tags --always 2>/dev/null || echo dev`
ldflags := "-s -w -X main.version=" + version
prefix := env_var_or_default("PREFIX", env_var("HOME") / ".local/bin")
scratch := "/tmp/stoat-scratch"

# show available recipes
default:
    @just --list

# build ./stoat
[group('build')]
build:
    go build -ldflags "{{ldflags}}" -o stoat ./cmd/stoat

# build and install to $PREFIX (default ~/.local/bin)
[group('build')]
install: build
    install -Dm755 stoat {{prefix}}/stoat
    @echo "installed {{prefix}}/stoat ({{version}})"
    @command -v stoat >/dev/null || echo "note: {{prefix}} is not on your PATH"

# build and install stoat, interactively: checks the host too
[group('build')]
setup: hooks
    go run ./cmd/installer

# build and install stoat without a TTY: for CI and scripts
[group('build')]
setup-headless: hooks
    go run ./cmd/installer --no-tty

# remove the installed binary: never touches ~/.stoat
[group('build')]
uninstall:
    rm -f {{prefix}}/stoat
    @echo "removed {{prefix}}/stoat, your VMs in ${STOAT_HOME:-~/.stoat} were left alone"

# run against your real ~/.stoat
[group('dev')]
run *args: build
    ./stoat {{args}}

# run against a throwaway data root, so dev cannot touch your real VMs
[group('dev')]
dev *args: build
    @mkdir -p {{scratch}}
    @echo "STOAT_HOME={{scratch}}"
    STOAT_HOME={{scratch}} ./stoat {{args}}

# delete the throwaway data root used by `just dev`
[group('dev')]
dev-reset:
    rm -rf {{scratch}}
    @echo "wiped {{scratch}}"

# run all tests
[group('test')]
test:
    go test ./...

# verbose tests for one package, e.g. `just test-pkg apkovl`
[group('test')]
test-pkg pkg:
    go test ./internal/{{pkg}}/ -v

# tests with the race detector
[group('test')]
race:
    go test -race ./...

# full lifecycle check on a real disk VM: install -> apply -> reboot -> mouse
# works. Needs KVM and network, ~15 min. Set STOAT_HOME to isolate it.
[group('test')]
e2e:
    ./scripts/e2e.sh

# coverage summary per package
[group('test')]
cover:
    go test -cover ./...

# exactly what .githooks/pre-commit runs
[group('dev')]
check:
    gofmt -l $(git ls-files "*.go")
    go vet ./...
    go build ./...

# exactly what the lint and shellcheck CI jobs run
[group('dev')]
lint:
    golangci-lint run ./...
    shellcheck -S warning $(git ls-files "internal/recipes/bundled/*.sh" "internal/recipes/bundled/*/*.sh" "scripts/*.sh" ".githooks/*")

# format in place
[group('dev')]
fmt:
    gofmt -w $(git ls-files "*.go")

# tidy go.mod and show the go directive
[group('dev')]
tidy:
    go mod tidy
    @grep '^go ' go.mod

# enable the pre-commit and commit-msg hooks
[group('dev')]
hooks:
    git config core.hooksPath .githooks
    @echo "hooks enabled: $(git config core.hooksPath)"

# what stoat knows about: VMs, ISOs, ports
[group('vms')]
vms:
    #!/usr/bin/env sh
    . "{{justfile_directory()}}/.just/fmt.sh"
    root="${STOAT_HOME:-$HOME/.stoat}"
    [ -d "$root" ] || { warn "no data root at $root"; exit 0; }
    head "vms" "$root"
    dim "$(printf '     %-16s %-6s %-9s %-9s %6s %5s' NAME MODE STATE RECIPES RAM SSH)"
    any=0
    for d in "$root"/*/; do
      [ -d "$d" ] || continue
      n=$(basename "$d")
      case "$n" in isos|recipes|logs) continue ;; esac
      any=1
      if [ ! -f "$d/vm.toml" ]; then
        printf "  %s  %-16s %s\n" "$(c 8 '?')" "$n" "$(c 8 'no vm.toml')"
        continue
      fi
      port=$(sed -n 's/^sshport *= *//p' "$d/vm.toml" | tr -d ' ')
      mode=$(sed -n 's/^mode *= *//p' "$d/vm.toml" | tr -d '" ')
      ram=$(sed -n 's/^ram *= *//p' "$d/vm.toml" | tr -d ' ')
      rec=$(sed -n 's/^recipes *= *//p' "$d/vm.toml" | tr -d '[]" ')
      [ -n "$mode" ] || { printf "  %s  %-16s %s\n" "$(c 1 '!')" "$n" "$(c 1 'broken vm.toml')"; continue; }
      if [ -f "$d/qemu.pid" ] && kill -0 "$(cat "$d/qemu.pid" 2>/dev/null)" 2>/dev/null; then
        dot=$(c 2 '●'); state=$(c 2 "$(printf '%-9s' running)")
      else
        dot=$(c 8 '○'); state=$(c 8 "$(printf '%-9s' stopped)")
      fi
      printf "  %s  %-16s %-6s %s %-9s %6s %5s\n" "$dot" "$n" "$mode" "$state" "${rec:--}" "${ram:-?}M" "${port:-?}"
    done
    [ "$any" = 1 ] || dim "  no vms yet: run 'just dev' and press n"
    echo
    head "isos" "$root/isos"
    ls -1sh "$root/isos" 2>/dev/null | sed '1d;s/^/  /' || dim "  none"

# tail stoat's log
[group('vms')]
logs n="50":
    @tail -n {{n}} "${STOAT_HOME:-$HOME/.stoat}/logs/stoat.log" 2>/dev/null || echo "no log yet"

# tail a VM's last provision run, e.g. `just provision-log alpine`
[group('vms')]
provision-log name n="40":
    #!/usr/bin/env sh
    . "{{justfile_directory()}}/.just/fmt.sh"
    f="${STOAT_HOME:-$HOME/.stoat}/{{name}}/last-provision.log"
    [ -f "$f" ] || { warn "no provision log for {{name}}: press p in the TUI first"; exit 0; }
    head "provision log" "{{name}}"
    tail -n {{n}} "$f" | sed 's/^/  /'
    echo
    dim "  $(wc -l < "$f") lines · $f"

# check host prerequisites
[group('vms')]
doctor:
    #!/usr/bin/env sh
    . "{{justfile_directory()}}/.just/fmt.sh"
    head "doctor" "$(uname -sr)"
    miss=0
    for b in qemu-system-x86_64 qemu-img ssh ssh-keygen go just; do
      if p=$(command -v "$b" 2>/dev/null); then ok "$b" "$p"; else bad "$b" "not found"; miss=$((miss+1)); fi
    done
    if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then ok /dev/kvm "readable and writable"
    else bad /dev/kvm "add yourself to the kvm group, then re-login"; miss=$((miss+1)); fi
    root="${STOAT_HOME:-$HOME/.stoat}"
    [ -d "$root" ] && ok "data root" "$root" || warn2 "data root" "$root (created on first run)"
    if command -v stoat >/dev/null; then ok "stoat on PATH" "$(command -v stoat) ($(stoat --version 2>/dev/null))"
    else warn2 "stoat on PATH" "not installed: run 'just install'"; fi
    echo
    [ "$miss" = 0 ] && printf "  %s all prerequisites present\n" "$(c 2 ✓)" || printf "  %s %s missing\n" "$(c 1 ✗)" "$miss"

# remove build artifacts (never touches your VMs)
[group('build')]
clean:
    rm -f stoat
    go clean -testcache

# tag a release; pushing the tag triggers the release workflow
[group('release')]
tag v:
    @git diff --quiet || { echo "working tree dirty"; exit 1; }
    git tag {{v}}
    @echo "created {{v}}, push with: git push origin {{v}}"
