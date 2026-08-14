.PHONY: build test lint deps proto test-ruleset

# Install tools the other targets assume are present.
deps:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2; }

# Regenerate the contract Go package from proto/. Requires buf + protoc-gen-go.
proto:
	buf lint
	buf generate

build:
	go build ./...

test:
	go test -race -v ./...

# Run the strict base ruleset's own .tln.test through the talon CLI tool. Also
# covered by `go test ./internal/ruleset/`, but handy on its own.
test-ruleset:
	go tool github.com/opentalon/talon-language/cmd/talon test \
		internal/ruleset/base/talooner.tln internal/ruleset/base/talooner.tln.test

lint:
	golangci-lint run
