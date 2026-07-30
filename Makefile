.PHONY: build install hooks test

build:
	go build -ldflags "-s -w -X main.version=$$(git describe --tags --always)" -o stoat ./cmd/stoat

install: build
	install -Dm755 stoat $(HOME)/.local/bin/stoat

hooks:
	git config core.hooksPath .githooks

test:
	go test ./...
