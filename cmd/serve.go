package cmd

import (
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/web"
	"github.com/spf13/cobra"
)

var (
	servePort   int
	serveNoOpen bool
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a web interface for browsing reports, prompts, and runs",
	Long: `Start a localhost-only read-only web server.

When run inside a project, shows only that project.
When run outside, lists all registered projects.

Example:
  ateam serve
  ateam serve --port 8080
  ateam serve --no-open`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "port to listen on (0 = random)")
	serveCmd.Flags().BoolVar(&serveNoOpen, "no-open", false, "do not open the browser automatically")
}

func runServe(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return err
	}

	srv, err := web.New(env)
	if err != nil {
		return err
	}
	defer srv.Close()

	return srv.ListenAndServe(servePort, !serveNoOpen)
}
