// Command climail is a terminal Gmail client: a TUI plus a scriptable CLI.
package main

import (
	"os"

	_ "time/tzdata" // named zones work without a system zoneinfo database

	"github.com/jabiOP/climail/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(cli.Main())
}
