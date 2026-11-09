package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/secret"
	"github.com/mattn/go-isatty"
)

// cmdContext returns the root context for long-running commands. Like
// signal.NotifyContext it cancels on SIGINT/SIGTERM, but it records which
// signal arrived as the cancel cause (agent.ErrSignalInterrupt /
// agent.ErrSignalTerminate) so failure classification can distinguish an
// interactive Ctrl-C from a parent process tearing the run down.
func cmdContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			if sig == syscall.SIGTERM {
				cancel(agent.ErrSignalTerminate)
			} else {
				cancel(agent.ErrSignalInterrupt)
			}
		case <-ctx.Done():
		}
	}()
	stop := func() {
		signal.Stop(sigCh)
		cancel(nil)
	}
	return ctx, stop
}

// errNoReview is the canonical error for commands that need the supervisor
// review file before they can proceed.
func errNoReview(reviewPath string) error {
	return fmt.Errorf("no review found at %s; run 'ateam review' first", reviewPath)
}

// ExitError is returned by commands that need to exit with a specific non-zero code.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func newTable() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

func relPath(cwd, path string) string {
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return path
	}
	return rel
}

// printArtifact prints the on-disk artifact at path for --print. The shipped
// single-file prompts (review, verify, report, auto_setup) instruct the agent
// to write its output via the Write tool and end with a one-line confirmation
// message — "the on-disk file is the source of truth, anything you stream as
// text is discarded." So --print must read the file, not the stream. The
// prompts' documented recovery path is "if Write fails, emit the body as the
// final message," so streamFallback is printed when the file is missing or
// empty. Quiet no-op when both are empty.
func printArtifact(path, streamFallback string) {
	if path != "" {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			fmt.Printf("\n%s", string(data))
			if data[len(data)-1] != '\n' {
				fmt.Println()
			}
			return
		}
	}
	if streamFallback != "" {
		fmt.Printf("\n%s\n", streamFallback)
	}
}

// resolveBatch picks the batch ID for a supervisor-driven run. When override
// is non-empty (the user passed --batch), it wins; otherwise the helper mints
// "<prefix>-<timestamp>" using the same TimestampFormat all the renderers
// agree on. Centralized so every supervisor entry point (code, parallel,
// report, run-all, ...) produces the same shape — and so prefix typos stay
// out of LatestBatch lookups downstream.
func resolveBatch(override, prefix string) string {
	if override != "" {
		return override
	}
	return prefix + "-" + time.Now().Format(display.TimestampFormat)
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// setSourceWritable marks the runner's container as source-writable.
// No-op if the runner has no container or the container type doesn't support it.
func setSourceWritable(r *runner.AgentExecutor) {
	if r.Container != nil {
		r.Container.SetSourceWritable(true)
	}
}

func isTerminal() bool {
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

// stdinIsPiped reports whether stdin is a pipe or redirected file rather than
// an interactive terminal. Commands use this to auto-read a piped prompt
// when no positional argument is given.
func stdinIsPiped() bool {
	fd := os.Stdin.Fd()
	return !isatty.IsTerminal(fd) && !isatty.IsCygwinTerminal(fd)
}

func secretResolver(env *root.ResolvedEnv, backend secret.Backend) *secret.Resolver {
	if env == nil {
		return secret.NewResolver("", backend, nil)
	}
	var opts *secret.ResolverOpts
	if env.KeychainKey != "" {
		opts = &secret.ResolverOpts{ProjectKeychainKey: env.KeychainKey}
	}
	return secret.NewResolver(env.ProjectDir, backend, opts)
}
