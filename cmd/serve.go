package cmd

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
	env, err := lookupEnv()
	if err != nil {
		return err
	}

	if env.ProjectDir != "" {
		db, err := requireStateDB(env)
		if err != nil {
			return err
		}
		db.Close()
	}

	host := "127.0.0.1"
	if serveBind != "" {
		host = serveBind
	} else if servePublic {
		host = "0.0.0.0"
	}

	port, portSource, err := resolveServePort(env, host)
	if err != nil {
		return err
	}

	srv, err := web.New(env)
	if err != nil {
		return err
	}
	defer srv.Close()
	srv.PortSource = portSource

	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		fmt.Fprintf(os.Stderr, "WARNING: Web UI is accessible from the network without authentication.\n")
	}

	enableJobControl()

	return srv.ListenAndServe(port, !serveNoOpen, host)
}

// resolveServePort decides which port to bind. Explicit --port and a
// configured Serve.Port take precedence. Otherwise, when running inside a
// project, the port is remembered in .ateam/cache/serve.port so successive
// `ateam serve` invocations reuse the same URL — letting the browser refocus
// the existing tab instead of opening a new one. Returns the port plus a
// human-readable source path to show in the startup banner (empty when not
// using the cache file).
func resolveServePort(env *root.ResolvedEnv, host string) (int, string, error) {
	if servePort > 0 {
		return servePort, "", nil
	}
	if env != nil && env.Config != nil && env.Config.Serve.Port > 0 {
		return env.Config.Serve.Port, "", nil
	}
	if env == nil || env.ProjectDir == "" {
		return 0, "", nil
	}

	portFile := filepath.Join(env.ProjectDir, "cache", "serve.port")
	display := portFile
	if env.SourceDir != "" {
		if rel, err := filepath.Rel(env.SourceDir, portFile); err == nil {
			display = rel
		}
	}

	if data, err := os.ReadFile(portFile); err == nil {
		if cached, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && cached > 0 && isPortFree(host, cached) {
			return cached, display, nil
		}
	}

	port, err := pickFreePort(host)
	if err != nil {
		return 0, "", fmt.Errorf("pick free port: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		return 0, "", fmt.Errorf("create cache dir: %w", err)
	}
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)+"\n"), 0o644); err != nil {
		return 0, "", fmt.Errorf("write %s: %w", portFile, err)
	}
	return port, display, nil
}

func isPortFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func pickFreePort(host string) (int, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// enableJobControl makes CTRL-Z (SIGTSTP) suspend the process so the shell
// can background it with `bg` and bring it back with `fg`. The Go runtime
// silently drops SIGTSTP in this binary, so we catch it and re-raise as
// SIGSTOP, which the kernel cannot let any handler swallow.
func enableJobControl() {
	tstp := make(chan os.Signal, 1)
	signal.Notify(tstp, syscall.SIGTSTP)
	go func() {
		for range tstp {
			_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		}
	}()
}
