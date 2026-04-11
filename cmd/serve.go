package cmd

import (
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/web"
	"github.com/spf13/cobra"
)

var (
	servePort   int
	serveNoOpen bool
	servePublic bool
	serveBind   string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start a web interface for browsing reports, prompts, and runs",
	Long: `Start a read-only web server (localhost-only by default).

When run inside a project, shows only that project.
When run outside, lists all registered projects.

Example:
  ateam serve
  ateam serve --port 8080
  ateam serve --no-open
  ateam serve --public --port 8080
  ateam serve --bind 192.168.1.50 --port 8080`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "port to listen on (0 = random)")
	serveCmd.Flags().BoolVar(&serveNoOpen, "no-open", false, "do not open the browser automatically")
	serveCmd.Flags().BoolVar(&servePublic, "public", false, "bind to 0.0.0.0 instead of 127.0.0.1 (allow access from other machines)")
	serveCmd.Flags().StringVar(&serveBind, "bind", "", "bind to a specific IP address (e.g. 192.168.1.50)")
}

func runServe(cmd *cobra.Command, args []string) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	// Verify the database exists before starting the server.
	db, err := requireProjectDB(env)
	if err != nil {
		return err
	}
	db.Close()

	port := servePort
	if port == 0 && env != nil && env.Config != nil && env.Config.Serve.Port > 0 {
		port = env.Config.Serve.Port
	}

	srv, err := web.New(env)
	if err != nil {
		return err
	}
	defer srv.Close()

	host := "127.0.0.1"
	if serveBind != "" {
		host = serveBind
	} else if servePublic {
		host = "0.0.0.0"
	}

	return srv.ListenAndServe(port, !serveNoOpen, host)
}
