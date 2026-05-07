package cmd

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/display"
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
	fmt.Printf("built:  %s\n", FormatBuildTime(BuildTime, time.Now()))

	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		fmt.Printf("system: %s", strings.TrimSpace(string(out)))
		fmt.Println()
	}

	return nil
}

// FormatBuildTime renders the BuildTime ldflag (a unix sub-second timestamp
// like "1715065850.123456" stamped by the Makefile) as
//
//	<ts> (<local time>) <duration> ago
//
// Returns the raw string unchanged when BuildTime can't be parsed (e.g.
// "unknown" in dev builds).
func FormatBuildTime(ts string, now time.Time) string {
	secs, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return ts
	}
	whole := int64(secs)
	nanos := int64((secs - float64(whole)) * float64(time.Second))
	t := time.Unix(whole, nanos)

	dur := display.FmtHoursOrDays(now.Sub(t), 48*time.Hour)
	return fmt.Sprintf("%s (%s) %s ago", ts, t.Local().Format("2006-01-02 15:04:05 MST"), dur)
}
