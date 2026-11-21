# climail

## Purpose

A fast terminal client for Gmail: a keyboard-driven TUI for reading and triaging mail across several Google
accounts, and a Unix-friendly CLI for scripts. Mail is cached locally, so reading is instant and works offline;
changes sync in the background.

## Usage

## Requirements

Go 1.26 or newer. Linux or macOS.

## Installation

## Development

Build:

```bash
make install   # go install .
```

Test:

```bash
make test      # go test -race ./...
```

Lint/format:

```bash
make lint      # gofmt check and go vet
```
