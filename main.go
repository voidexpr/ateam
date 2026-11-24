package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/ateam/cmd"
	"github.com/fencesandbox/fence/pkg/fence"
)

func main() {
	// On Linux the embedded fence sandbox starts this binary again, inside
	// the sandbox, in private modes of its own: they come before ateam's
	// own arguments. Any other argument is not handled here.
	if handled, code, err := fence.DispatchInternalHelper(os.Args); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
	if err := cmd.Execute(); err != nil {
		var exitErr *cmd.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
