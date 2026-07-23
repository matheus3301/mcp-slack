# mcp-slack — development and verification targets.
#
# Tool versions are pinned so local runs match CI exactly. `go run <tool>@<ver>`
# fetches and caches the tool on first use; no global install is required.

GO ?= go
VERSION ?= dev

STATICCHECK_VERSION ?= v0.7.0
GOVULNCHECK_VERSION ?= v1.6.0
GOSEC_VERSION       ?= v2.28.0

GOOS   ?= linux
GOARCH ?= amd64

.PHONY: all check fmt fmt-check vet staticcheck lint test test-race \
        govulncheck gosec security build tidy clean tools

all: check

## check: run the full local verification pipeline
check: fmt-check vet staticcheck test test-race security build

## fmt: format all Go source
fmt:
	gofmt -w .

## fmt-check: fail if any file is not gofmt-clean
fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

## vet: run go vet
vet:
	$(GO) vet ./...

## staticcheck: run staticcheck (pinned)
staticcheck:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

## lint: vet + staticcheck
lint: vet staticcheck

## test: run unit and integration tests
test:
	$(GO) test ./...

## test-race: run tests under the race detector
test-race:
	$(GO) test -race ./...

## govulncheck: scan for known vulnerabilities (pinned)
govulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

## gosec: static security analysis (pinned)
gosec:
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) -exclude-dir=.tools ./...

## security: govulncheck + gosec
security: govulncheck gosec

## build: build a stripped, reproducible binary into dist/
build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/mcp-slack ./cmd/mcp-slack

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

## clean: remove build output
clean:
	rm -rf dist
