# go-lighter — developer tasks.
# No target here touches the network except `smoke` (keyless, read-only).

GO ?= go

.PHONY: all fmt fmt-check vet lint test race bench vectors smoke tidy check

all: check

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

test:
	$(GO) test ./... -count=1

race:
	$(GO) test ./... -count=1 -race

# Hot-path benchmarks. House rule: a change on a hot path ships with before/after
# numbers (median of >= 3 runs, ns/op + allocs/op); allocs/op must not grow.
bench:
	$(GO) test ./types/... ./internal/... -run '^$$' -bench . -benchmem -count=3

# Regenerates internal/tx/vectors_generated_test.go with the OFFICIAL Go SDK
# (elliottech/lighter-go). scripts/gen-vectors is a separate module so that
# lighter-go (and go-ethereum behind it) never enters this module's graph.
vectors:
	cd scripts/gen-vectors && $(GO) run . > ../../internal/tx/vectors_generated_test.go
	gofmt -w internal/tx/vectors_generated_test.go

# Keyless, read-only run against the public mainnet API.
smoke:
	$(GO) run ./examples/market-data

tidy:
	$(GO) mod tidy

check: fmt-check vet race
