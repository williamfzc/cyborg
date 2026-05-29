// Command entrypoint for the Cyborg CLI.
// It forwards process arguments into the internal CLI package.
package main

import (
	"os"

	"github.com/williamfzc/cyborg/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
