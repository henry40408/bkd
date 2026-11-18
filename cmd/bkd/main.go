// Command bkd is a single-binary bookmark manager.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/henry40408/bkd/internal/version"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr))
}

// run returns the exit code. The log goes to stderr, with the setup token.
func run(args []string, lookupEnv func(string) (string, bool), stdout, stderr io.Writer) int {
	// First, before flags: a retired variable must stop startup however the
	// rest of the command line looks.
	if err := ensureNoLegacyEnvVars(lookupEnv); err != nil {
		return reportFatal(stderr, err)
	}
	opts, err := parseOptions(args, lookupEnv, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return reportFatal(stderr, err)
	}
	if opts.showVersion {
		_, _ = fmt.Fprintf(stdout, "bkd %s\n", version.Version())
		return exitOK
	}
	if err := serve(opts, newLogger(stderr, opts.logLevel)); err != nil {
		return reportFatal(stderr, err)
	}
	return exitOK
}

func reportFatal(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
	var usage usageError
	if errors.As(err, &usage) {
		return exitUsage
	}
	return exitFailure
}
