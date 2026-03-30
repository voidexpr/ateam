package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, build, and system information",
	Args:  cobra.NoArgs,
	RunE:  runVersion,
}

func runVersion(cmd *cobra.Command, args []string) error {
	fmt.Printf("ateam:  %s\n", Version)
	fmt.Printf("commit: %s\n", GitCommit)
	fmt.Printf("built:  %s\n", BuildTime)

	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		fmt.Printf("system: %s", strings.TrimSpace(string(out)))
		fmt.Println()
	}

	return nil
}
