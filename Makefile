.PHONY: install test lint

BIN := $(or $(shell go env GOBIN),$(shell go env GOPATH)/bin)

# install builds and installs climail, then restarts its daemon (if installed) on the new version.
install:
	go install .
	"$(BIN)/climail" daemon restart

test:
	go test -race ./...

lint:
	test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)
	go vet ./...
