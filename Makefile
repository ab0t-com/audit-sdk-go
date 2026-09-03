# ab0t Audit Service Go SDK

GO ?= go

.PHONY: help
help:
	@echo "test    - run the test suite (-race)"
	@echo "vet     - go vet"
	@echo "fmt     - gofmt -l -w"
	@echo "cover   - test with a coverage report"
	@echo "check   - fmt-check + vet + test + stdlib-only assertion (what CI runs)"

.PHONY: test
test:
	$(GO) test -race ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: fmt
fmt:
	gofmt -l -w .

.PHONY: cover
cover:
	$(GO) test -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out | tail -1

# stdlib-only is a hard property of this module, not a preference: it is embedded
# in other people's binaries, so a dependency here becomes a dependency everywhere.
.PHONY: check
check:
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt: files need formatting (run make fmt)"; exit 1)
	$(GO) vet ./...
	$(GO) test -race ./...
	@test ! -f go.sum || (echo "go.sum present: a dependency crept in (this module must be stdlib-only)"; exit 1)
	@echo "OK: stdlib-only, vetted, tested"
