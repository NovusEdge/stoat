version := `git describe --tags --always`

# list recipes
default:
    @just --list

build:
    go build -ldflags "-s -w -X main.version={{version}}" -o stoat ./cmd/stoat

install: build
    install -Dm755 stoat ~/.local/bin/stoat

hooks:
    git config core.hooksPath .githooks

test:
    go test ./...

# what the pre-commit hook runs
check:
    gofmt -l .
    go vet ./...
    go build ./...

run *args: build
    ./stoat {{args}}
