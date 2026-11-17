// Command bkd is a single-binary bookmark manager.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/henry40408/bkd/internal/version"
)

const (
	exitOK    = 0
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run returns the process exit code. It only knows --version until the server
// is ported.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("bkd", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if !*showVersion {
		flags.Usage()
		return exitUsage
	}
	_, _ = fmt.Fprintf(stdout, "bkd %s\n", version.Version())
	return exitOK
}
