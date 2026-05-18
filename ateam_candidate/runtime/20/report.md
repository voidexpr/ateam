# Summary

This is the first security audit for `ateam` under the `project.security` role. The codebase is mature in its handling of the highest-risk areas: secrets are read from stdin, OS keychain, or 0600 files; secret values reach child processes via `cmd.Env` (not argv) explicitly to avoid `ps aux` leaks; `os/exec` calls all use fixed argv with no shell interpolation; SQL queries use parameterized placeholders; the web UI is read-only and binds to localhost by default with a network-exposure warning. One real defect found (process-table leak via the `ateam secret --value` flag) plus a couple of low-priority informational items.

# Role performing the audit

- Role: `project.security`
- Model: Claude Opus 4.7 (`claude-opus-4-7`) — no extended thinking, default effort
- Mode: read-only static analysis; no tools run (Go has no required `gosec` data point flagged by the role)
- Prior report: none — `.ateam/roles/project.security/history/` is empty, runtime/20 starts fresh.

# Findings

## 1. `ateam secret --value <SECRET>` leaks the secret value via the process table

- **Location**: `cmd/secret.go:56` (flag definition) and `cmd/secret.go:179-189` (consumed by `setSecret`).
- **Severity**: MEDIUM
- **Effort**: SMALL

- **Description**: The `secret` subcommand defines a `--value` flag bound to `secretValue`. When a user runs `ateam secret SOME_KEY --set --value sk-xxxx`, the secret value sits in the process arguments and is visible to any local user via `ps auxww`, in shell history, in audit logs, and in supervisord/launchd argv captures. The default code path (stdin / interactive paste) is fine, and the `Long` help in `cmd/secret.go:32-46` correctly demonstrates the stdin form; the `--value` flag is registered without any usage warning and is not mentioned in `Long`. The trigger is the explicit user choice of `--value`, but the failure mode — secret reaches argv — is the exact class of leak the rest of the codebase carefully avoids (see `internal/container/docker.go:376-398` and `internal/container/container.go:120-149`, where `-e KEY=VALUE` was deliberately split into `-e KEY` argv plus `cmd.Env` value for this reason).

- **Recommendation**: Remove the `--value` flag, or replace it with `--value-from-env <ENVVAR>` (the secret stays in the env, never in argv); alternately, replace `--value` with `--from-file <path>` so scripted callers can pipe via a 0600 tempfile. If the flag must stay for backward compatibility, print a one-line warning to stderr whenever `--value` is non-empty and document the risk in `Long`. The `--get` and `--print` paths (`cmd/secret.go:147,268`) are not findings — they emit values to stdout under explicit user request and rely on the caller's redirection.

## 2. CSP allows `'unsafe-inline'` for `script-src` and `style-src` (defense-in-depth)

- **Location**: `internal/web/server.go:291-298`.
- **Severity**: LOW
- **Effort**: MEDIUM

- **Description**: `securityHeaders` sets `script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'`. This is defense-in-depth, not an active vulnerability: goldmark is constructed without `html.WithUnsafe()` (see `internal/web/markdown.go:67-73`), so user-supplied markdown cannot inject raw `<script>` tags, and the server is bound to `127.0.0.1` by default (`cmd/serve.go:69-78`) with an explicit "accessible without authentication" warning when `--public` or `--bind` is used. Tightening to nonces would require verifying every embedded template, the Chroma syntax-highlighter output, and the export HTML still render correctly without inline styles/scripts. Filed for visibility only — per the role's hard rule on CSP changes, this is flagged for human review rather than an actionable recommendation, because (a) I cannot prove the templates do not rely on inline JS/CSS without running the UI, (b) there is no fallback plan if a tightened CSP breaks the dashboard, and (c) the localhost default + warning is the compensating control.

- **Recommendation**: No action without explicit owner sign-off. If revisited later, the verification step is "load `ateam serve`, click through every nav tab (overview, reports, review, verify, runs, prompts, cost, sessions, code), confirm no console CSP violations and no visual regressions in Chroma-highlighted code blocks." The fallback is to revert the header to the current value.

## 3. `dirName` URL segment not filtered for `..` in code-session handlers (bounded to projectDir)

- **Location**: `internal/web/handlers.go:1301-1413` (`handleCodeSessionDetail` and `handleCodeSessionFile`), via `codeSessionDirs` at `internal/web/handlers.go:1176-1182`.
- **Severity**: LOW
- **Effort**: SMALL

- **Description**: The `{session}` URL path value is used directly in `filepath.Join(projectDir, "supervisor", "code", dirName)`. `fileName` is properly rejected when it contains `..` or `/` (handlers.go:1387), but `dirName` is not. The final `isPathWithin(absPath, pe.ProjectDir)` check at line 1397 contains any traversal to within `projectDir` (i.e., the `.ateam/` directory). Since the entire `.ateam/` tree is already exposed by design through the other read-only handlers, there is no real escalation — a caller cannot read files outside `.ateam/` and is further constrained to `.md` files. The defect is scope creep (a `/p/<slug>/code/../<file>.md` URL can resolve to `.ateam/<file>.md` which is not a code-session file) rather than a confidentiality breach. The web server is also localhost-only by default with an explicit network-exposure warning.

- **Recommendation**: Reject `dirName` values containing `..`, `/`, or starting with `.` at the top of both handlers, mirroring the existing fileName guard. This is consistency, not exploit prevention.

# Quick Wins

1. **Finding #1** (MEDIUM, SMALL): drop the `ateam secret --value` flag or rename to `--value-from-env`; if kept, warn on stderr and document the leak in `Long`.
2. **Finding #3** (LOW, SMALL): add a `..`/`/` reject on `dirName` in `handleCodeSessionDetail`/`handleCodeSessionFile` to match the existing `fileName` guard.

(Two quick wins is fine — the report deliberately does not pad with LOW defense-in-depth items.)

# Project Context

- **Codebase type**: Go CLI (`main.go`, `cmd/`, `internal/`) plus an embedded read-only web UI for browsing run artefacts. Built/tested via `make build` / `make test` / `make test-docker`.
- **Primary security boundaries**:
  - Secret storage: `internal/secret/store.go` (FileStore at 0600, parent dir 0700; keychain via `github.com/zalando/go-keyring`); resolver chain project → org → global → env in `internal/secret/resolve.go`.
  - Process-table hygiene: `internal/container/container.go:120-149` (`stagedEnv`) and `internal/container/docker.go:376-398` / `internal/container/docker_exec.go:285-305` (`envArgs`) — secret values reach docker via `cmd.Env`, only `-e KEY` (no value) on argv. Documented at `internal/container/docker.go:376-380`.
  - Prompt delivery: `internal/agent/claude.go:106` (`cmd.Stdin = strings.NewReader(req.Prompt)`) — prompts go via stdin, never argv.
  - Web UI: localhost by default in `cmd/serve.go:69-78`; CSP, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` in `internal/web/server.go:291-298`; path-confinement via `isPathWithin` in `internal/web/handlers.go:651-656`; safe-by-default goldmark (no `WithUnsafe`) in `internal/web/markdown.go:67-73`.
  - SQLite state: `internal/calldb/calldb.go:122-129` pre-creates the DB at 0600; `internal/calldb/queries.go:327-374` uses parameterized `?` placeholders even in dynamic `IN (?,?,…)` lists — no string-interpolated SQL.
  - Subprocess invocation: `git`, `docker`, `cp`, `claude`, `security` all called via `exec.Command(name, args...)` with fixed argv. Shell helpers in `cmd/agent_config.go:730,760` correctly use `sh -c '<script>' "_" "$@"` positional passing (no interpolation of untrusted strings into the script body).
- **Notable existing security work** (do not re-flag):
  - `internal/container/docker.go:376-380` comment explicitly documents the argv-vs-env split for secrets.
  - `internal/agent/claude_auth.go:289-321` (`BuildCleanEnv`) strips conflicting auth env vars; `internal/secret/validate.go:97-153` (`IsolateCredentials`) prevents credential confusion at the agent layer.
  - `cmd/serve.go:76-78` warns when binding off-localhost — the read-only-without-auth localhost default is a documented choice; do not propose adding auth in future cycles without owner confirmation that remote usage is a goal.
- **Files/dirs to revisit in future cycles**: `cmd/secret.go` (resolution of finding #1), `internal/web/handlers.go` code-session handlers (finding #3), and the auto_setup/code/review/code session flows when new HTTP routes are added under `internal/web/server.go:240-258`.
- **Out of scope for this role**: dependency CVEs (handled by `project.dependencies`); coverage gaps; and feature/design recommendations.
