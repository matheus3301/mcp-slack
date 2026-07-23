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
        govulncheck gosec security build dist verify-dist tidy clean tools

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

## dist: build all release archives + SHA256SUMS into dist/ (mirrors CI)
dist:
	VERSION=$(VERSION) COMMIT=$$(git rev-parse HEAD 2>/dev/null) \
		DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
		GO=$(GO) ./scripts/package.sh dist

## verify-dist: check every archive against dist/SHA256SUMS
verify-dist:
	@cd dist && (command -v sha256sum >/dev/null 2>&1 && sha256sum -c SHA256SUMS \
		|| shasum -a 256 -c SHA256SUMS)

## tidy: sync go.mod/go.sum
tidy:
	$(GO) mod tidy

## clean: remove build output
clean:
	rm -rf dist
