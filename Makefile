.PHONY: build test lint deps proto test-ruleset

# opentalon core's plugin fetcher (internal/bundle/fetch.go) shells out to
# `make build BINARY_NAME=<name>` when a Makefile is present, and expects a
# runnable binary at that path afterward. Default matches what it passes for
# this plugin; override for a local build under a different name.
BINARY_NAME ?= talooner-plugin

# Install tools the other targets assume are present.
deps:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2; }

# Regenerate the contract Go package from proto/. Requires buf + protoc-gen-go.
proto:
	buf lint
	buf generate

# go build ./... alone compiles every package but writes no binary to disk
# when it matches more than one main package — silently useless as a plugin
# build target. -o + a single package target is what actually produces the
# runnable binary core's fetcher expects.
build:
	go build -o $(BINARY_NAME) ./cmd/talooner-plugin

test:
	go test -race -v ./...

# Run the strict base ruleset's own .tln.test through the tln CLI tool. Also
# covered by `go test ./internal/ruleset/`, but handy on its own.
test-ruleset:
	go tool github.com/opentalon/tln-language/cmd/tln test \
		internal/ruleset/base/talooner.tln internal/ruleset/base/talooner.tln.test

lint:
	golangci-lint run
