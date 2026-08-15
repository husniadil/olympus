// Command olympus drives real terminal sessions from the command line.
package main

import (
	"context"
	"os"

	"github.com/husniadil/olympus/internal/cli"
)

func main() {
	os.Exit(cli.Execute(context.Background(), os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}
