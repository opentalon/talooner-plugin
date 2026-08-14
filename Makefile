.PHONY: build test lint deps

# Install tools the other targets assume are present.
deps:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Installing golangci-lint..."; go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.7.2; }

build:
	go build ./...

test:
	go test -race -v ./...

lint:
	golangci-lint run
