// Package tmuxctl provides a small wrapper around the tmux binary.
package tmuxctl

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// serverGraceDelay is the time exec.Cmd.WaitDelay gives the tmux server to
// shut down after ctx cancellation before pipes are force-closed. Matches
// the agent package's processGraceDelay (kept independent to avoid the
// tmuxctl → agent import cycle).
const serverGraceDelay = 30 * time.Second

// Codex and other TUIs render differently depending on terminal size. The
// default has to be wide enough that Codex's bottom input box and status line
// do not wrap during prompt detection, and tall enough that the response
// scrollback isn't unnecessarily fragmented. 300x100 follows oauth-cli-coder's
// tested choice and tracks long `/review` output without column wrapping.
const (
	DefaultWidth  = 300
	DefaultHeight = 100
)

// CmdFactory matches the command factory used by the container package without
// making tmuxctl depend on containers.
type CmdFactory func(ctx context.Context, name string, args ...string) *exec.Cmd

// Session is a single tmux session target.
type Session struct {
	Name         string
	SocketPath   string
	Width        int
	Height       int
	cmdFactory   CmdFactory
	env          []string
	scriptPath   string
	serverCmd    *exec.Cmd
	serverOutput bytes.Buffer
}

// New creates a detached tmux session running cmd. socketPath is the absolute
// path where the per-session unix socket will live; its parent directory is
// created (mode 0700) if missing. The caller is responsible for choosing a
// path that's unique per concurrent session — codex-tmux derives it from
// project dir and EXEC_ID.
func New(ctx context.Context, socketPath, name string, width, height int, env []string, workdir string, cmd []string, factory CmdFactory) (*Session, error) {
	if name == "" {
		return nil, fmt.Errorf("tmux session name is required")
	}
	if socketPath == "" {
		return nil, fmt.Errorf("tmux socket path is required")
	}
	if len(cmd) == 0 || cmd[0] == "" {
		return nil, fmt.Errorf("tmux session command is required")
	}
	if width <= 0 {
		width = DefaultWidth
	}
	if height <= 0 {
		height = DefaultHeight
	}

	baseEnv := os.Environ()
	scrubbedEnv := scrubTmuxEnv(baseEnv)
	if len(scrubbedEnv) == len(baseEnv) {
		scrubbedEnv = nil
	}

	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		return nil, fmt.Errorf("create tmux socket dir: %w", err)
	}

	s := &Session{
		Name:       name,
		SocketPath: socketPath,
		Width:      width,
		Height:     height,
		cmdFactory: factory,
		env:        scrubbedEnv,
	}
	sessionCommand := shellCommand(cmd)
	if factory == nil {
		scriptPath := filepath.Join(filepath.Dir(socketPath), "cmd-"+safeSocketName(name)+".sh")
		if err := writeCommandScript(scriptPath, env, workdir, cmd); err != nil {
			return nil, err
		}
		s.scriptPath = scriptPath
		sessionCommand = shellCommand([]string{"sh", "-i"})
	}

	args := []string{"new-session", "-d", "-s", name, "-x", strconv.Itoa(width), "-y", strconv.Itoa(height)}
	if factory != nil {
		for _, kv := range env {
			if kv != "" {
				args = append(args, "-e", kv)
			}
		}
	}
	if workdir != "" && factory != nil {
		args = append(args, "-c", workdir)
	}
	args = append(args, sessionCommand)
	if factory == nil {
		if err := s.startServer(ctx); err != nil {
			return nil, err
		}
		if err := s.run(ctx, nil, args...); err != nil {
			ok, hasErr := s.HasSession(ctx)
			if hasErr != nil || !ok {
				s.cleanupServer()
				return nil, err
			}
		}
		if err := sleepReady(ctx, 600*time.Millisecond); err != nil {
			s.cleanupServer()
			return nil, err
		}
		if err := s.SendLiteral(ctx, shellCommand([]string{"sh", s.scriptPath})); err != nil {
			s.cleanupServer()
			return nil, err
		}
		if err := s.SendKeys(ctx, "Enter"); err != nil {
			s.cleanupServer()
			return nil, err
		}
	} else {
		if err := s.run(ctx, nil, args...); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// SendKeys sends tmux key names such as Enter or C-c.
func (s *Session) SendKeys(ctx context.Context, keys ...string) error {
	args := []string{"send-keys", "-t", s.Name}
	args = append(args, keys...)
	return s.run(ctx, nil, args...)
}

// SendLiteral pastes literal text into the session using a tmux paste buffer.
// This preserves multi-line text and avoids send-keys parsing the content as
// tmux key names.
func (s *Session) SendLiteral(ctx context.Context, text string) error {
	buffer := s.Name + "-input"
	if err := s.run(ctx, nil, "set-buffer", "-b", buffer, text); err != nil {
		return s.run(ctx, nil, "send-keys", "-l", "-t", s.Name, text)
	}
	if err := s.run(ctx, nil, "paste-buffer", "-b", buffer, "-t", s.Name); err != nil {
		return err
	}
	_ = s.run(ctx, nil, "delete-buffer", "-b", buffer)
	return nil
}

// TypeLiteral sends literal text as keystrokes. It is useful for TUIs that
// treat bracketed paste differently from typed input.
func (s *Session) TypeLiteral(ctx context.Context, text string) error {
	return s.run(ctx, nil, "send-keys", "-l", "-t", s.Name, text)
}

// PanePID returns the foreground process PID running in the session's pane.
// For codex-tmux this is the codex process itself (not the tmux server),
// which is what the runner records in agent_execs.pid so its liveness probe
// reports the real agent state.
func (s *Session) PanePID(ctx context.Context) (int, error) {
	out, err := s.output(ctx, "display-message", "-p", "-t", s.Name, "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	pidStr := strings.TrimSpace(out)
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("tmux pane_pid: parse %q: %w", pidStr, err)
	}
	return pid, nil
}

// Capture returns the rendered pane contents. When full is true, scrollback is
// included from the beginning through the visible region.
func (s *Session) Capture(ctx context.Context, full bool) (string, error) {
	args := []string{"capture-pane", "-p", "-J", "-t", s.Name}
	if full {
		args = append(args, "-S", "-", "-E", "-")
	}
	return s.output(ctx, args...)
}

// HasSession reports whether the tmux session still exists.
func (s *Session) HasSession(ctx context.Context) (bool, error) {
	err := s.run(ctx, nil, "has-session", "-t", s.Name)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "can't find session") ||
		strings.Contains(err.Error(), "no server running") ||
		strings.Contains(err.Error(), "No such file or directory") {
		return false, nil
	}
	return false, err
}

// Kill terminates the tmux session. Missing sessions are treated as already
// cleaned up.
func (s *Session) Kill(ctx context.Context) error {
	err := s.run(ctx, nil, "kill-session", "-t", s.Name)
	if err != nil {
		if strings.Contains(err.Error(), "can't find session") ||
			strings.Contains(err.Error(), "no server running") ||
			strings.Contains(err.Error(), "No such file or directory") {
			s.cleanupServer()
			return nil
		}
		return err
	}
	s.cleanupServer()
	return nil
}

func sleepReady(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (s *Session) startServer(ctx context.Context) error {
	// -u forces UTF-8 regardless of locale so pane captures contain
	// the Codex border glyphs and the `›` prompt prefix verbatim.
	tmuxArgs := []string{"-u", "-D", "-S", s.SocketPath}
	cmd := exec.CommandContext(ctx, "tmux", tmuxArgs...)
	// Put the server in its own process group so ctx-cancel SIGTERMs the
	// whole tree (server + every codex/shell process running in its panes)
	// in a single syscall. This is what makes a stray Ctrl-C from the
	// operator cleanly tear down the codex-tmux run without leaving
	// orphaned processes — same pattern claude.go / codex.go use for their
	// agent CLI invocations.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = serverGraceDelay
	if s.env != nil {
		cmd.Env = s.env
	}
	cmd.Stdout = &s.serverOutput
	cmd.Stderr = &s.serverOutput
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tmux %s: %w", strings.Join(tmuxArgs, " "), err)
	}
	s.serverCmd = cmd
	return nil
}

func (s *Session) cleanupServer() {
	if s.serverCmd != nil && s.serverCmd.Process != nil {
		done := make(chan struct{})
		go func() {
			_ = s.serverCmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = s.serverCmd.Process.Kill()
			<-done
		}
	}
	_ = os.Remove(s.SocketPath)
	_ = os.Remove(s.scriptPath)
}

func (s *Session) run(ctx context.Context, stdin *strings.Reader, args ...string) error {
	var input *strings.Reader
	if stdin != nil {
		input = stdin
	}
	_, err := s.command(ctx, input, args...)
	return err
}

func (s *Session) output(ctx context.Context, args ...string) (string, error) {
	return s.command(ctx, nil, args...)
}

func (s *Session) command(ctx context.Context, stdin *strings.Reader, args ...string) (string, error) {
	// -u: force UTF-8 to keep border glyphs and the `›` prompt intact in
	// captures regardless of the locale tmux inherited. SocketPath is
	// required by New(), so the per-session -S flag is unconditional.
	tmuxArgs := append([]string{"-u", "-S", s.SocketPath}, args...)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if stdin != nil {
			_, _ = stdin.Seek(0, 0)
		}
		out, err := s.commandOnce(ctx, stdin, tmuxArgs...)
		if err == nil {
			return out, nil
		}
		if !transientSocketError(out) || time.Now().After(deadline) {
			detail := strings.TrimSpace(out)
			if detail == "" {
				detail = strings.TrimSpace(s.serverOutput.String())
			}
			return out, fmt.Errorf("tmux %s: %w: %s", strings.Join(tmuxArgs, " "), err, detail)
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *Session) commandOnce(ctx context.Context, stdin *strings.Reader, args ...string) (string, error) {
	var cmd *exec.Cmd
	if s.cmdFactory != nil {
		cmd = s.cmdFactory(ctx, "tmux", args...)
	} else {
		cmd = exec.CommandContext(ctx, "tmux", args...)
	}
	if cmd.Env == nil {
		cmd.Env = s.env
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func transientSocketError(out string) bool {
	return strings.TrimSpace(out) == "" ||
		strings.Contains(out, "No such file or directory") ||
		strings.Contains(out, "Connection refused")
}

func safeSocketName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func shellCommand(cmd []string) string {
	return shellJoin(cmd)
}

func writeCommandScript(path string, env []string, workdir string, cmd []string) error {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if workdir != "" {
		b.WriteString("cd ")
		b.WriteString(shellQuote(workdir))
		b.WriteString(" || exit $?\n")
	}
	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !validShellName(key) {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(shellQuote(value))
		b.WriteByte('\n')
	}
	body := b.String() +
		shellJoin(cmd) + "\n" +
		"status=$?\n" +
		"printf '\\n[ateam tmux command exited %s]\\n' \"$status\"\n" +
		"sleep 86400\n"
	return os.WriteFile(path, []byte(body), 0600)
}

func validShellName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func shellJoin(cmd []string) string {
	parts := make([]string, 0, len(cmd))
	for _, arg := range cmd {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func scrubTmuxEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && (key == "TMUX" || key == "TMUX_PANE") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
