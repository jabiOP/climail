.PHONY: install test lint vuln

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

# vuln checks the code and its dependencies against the Go vulnerability database (needs the network).
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.10.0 ./...
