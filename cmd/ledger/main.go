// Command ledger is the accounting ledger CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	app := newApp(os.Stdout, os.Stderr)
	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newApp(stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "ledger",
		Usage:     "accounting ledger CLI",
		Writer:    stdout,
		ErrWriter: stderr,
		Commands: []*cli.Command{
			newBookRunCommand(stdout),
			newBenchCommand(stdout),
			newCloseCommand(stdout),
			newSeedCommand(stdout),
			newPolicyCommand(stdout),
			newTUICommand(),
		},
	}
}
