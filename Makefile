# swiftbon.shared — money model, FX types, and the cross-service contracts.
#
# The default target is `check`, which is what CI runs and what you should run
# before pushing: this module is the foundation for a money path, so a failing
# test here is not a broken build, it is a wrong ledger downstream.

GO ?= go
PROTOC ?= protoc
PROTO_DIR := proto
GEN_DIR := $(PROTO_DIR)/gen/go
PROTO_FILES := $(shell find $(PROTO_DIR) -name '*.proto' -not -path '$(GEN_DIR)/*')
COVERAGE_MIN := 90

.DEFAULT_GOAL := check
.PHONY: check test test-cover coverage-gate build vet fmt fmt-check lint tidy audit generate-protoc protoc-tools clean help

## check: build, vet, and test with the race detector and coverage gate
check: build vet test-cover coverage-gate

## build: compile every package
build:
	$(GO) build ./...

## vet: run go vet
vet:
	$(GO) vet ./...

## fmt: rewrite sources with gofmt
fmt:
	gofmt -s -w .

## fmt-check: fail if anything is not gofmt-clean
fmt-check:
	@unformatted=$$(gofmt -s -l . | grep -v '^$(GEN_DIR)/' || true); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

## test: run the tests with the race detector
test:
	$(GO) test -race ./...

## test-cover: run the tests with the race detector and write a coverage profile
test-cover:
	$(GO) test -race -covermode=atomic -coverprofile=coverage.out ./money/... ./fx/... ./account/... ./principal/... ./events/... ./refs/... ./names/... ./settings/
	@$(GO) tool cover -func=coverage.out | tail -1

## coverage-gate: fail if coverage falls below COVERAGE_MIN
coverage-gate: coverage.out
	@total=$$($(GO) tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$$3); print $$3}'); \
	echo "coverage: $$total% (minimum $(COVERAGE_MIN)%)"; \
	awk -v t="$$total" -v m="$(COVERAGE_MIN)" 'BEGIN { exit (t+0 >= m+0) ? 0 : 1 }' || { \
		echo "coverage $$total% is below the $(COVERAGE_MIN)% minimum for the money path"; exit 1; }

coverage.out:
	$(MAKE) test-cover

## coverage-html: open the coverage profile in a browser
coverage-html: coverage.out
	$(GO) tool cover -html=coverage.out

## protoc-tools: install the protoc plugins at the versions this module pins
protoc-tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

## generate-protoc: regenerate the Go bindings; the output is committed
generate-protoc:
	@command -v $(PROTOC) >/dev/null 2>&1 || { \
		echo "protoc not found. Install it (brew install protobuf) and run 'make protoc-tools'."; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || { \
		echo "protoc-gen-go not found on PATH. Run 'make protoc-tools' and ensure \$$(go env GOPATH)/bin is on PATH."; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null 2>&1 || { \
		echo "protoc-gen-go-grpc not found on PATH. Run 'make protoc-tools' and ensure \$$(go env GOPATH)/bin is on PATH."; exit 1; }
	rm -rf $(GEN_DIR)
	mkdir -p $(GEN_DIR)
	$(PROTOC) \
		--proto_path=$(PROTO_DIR) \
		--go_out=$(GEN_DIR) --go_opt=module=swiftbon.com/shared/proto/gen/go \
		--go-grpc_out=$(GEN_DIR) --go-grpc_opt=module=swiftbon.com/shared/proto/gen/go \
		$(PROTO_FILES)
	$(GO) build ./...

## generate-check: fail if the committed generated Go is stale
generate-check: generate-protoc
	@if [ -n "$$(git status --porcelain $(GEN_DIR) 2>/dev/null)" ]; then \
		echo "generated Go is out of date; run 'make generate-protoc' and commit the result"; \
		git --no-pager diff --stat $(GEN_DIR); exit 1; \
	fi

## tidy: tidy go.mod and go.sum
tidy:
	$(GO) mod tidy

## audit: verify dependencies and scan for known vulnerabilities
audit:
	$(GO) mod verify
	@echo "--- dependency surface (direct) ---"
	@$(GO) list -m -f '{{if not .Indirect}}{{.Path}} {{.Version}}{{end}}' all | grep -v '^swiftbon.com/shared' || true
	@command -v govulncheck >/dev/null 2>&1 || $(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	govulncheck ./...

## clean: remove build and coverage artefacts
clean:
	rm -f coverage.out
	$(GO) clean -cache -testcache

## help: list the targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | awk -F': ' '{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
