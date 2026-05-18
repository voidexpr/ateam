# Summary

Second `project.security` pass. The three findings from the prior cycle are all still open — no security-relevant commits landed between reports. One additional finding surfaced while re-walking the agent layer: `internal/agent/codex.go` passes the full prompt as a positional argv argument, putting the entire prompt content into the process table on every codex run; this is the exact leak class the rest of the codebase (and the parallel `claude.go` path) explicitly avoids by using stdin and `cmd.Env`.

# Role performing the audit

- Role: `project.security`
- Model: Claude Opus 4.7 (`claude-opus-4-7`) — no extended thinking, default effort
- Mode: read-only static analysis; no SAST tools run (Go, project already covered by `gosec`-class checks in dev/CI per role guidance)
- Prior reports: one — runtime/20 (2026-05-14_14-11-43). All three prior findings re-validated against current HEAD (`6dcf9a0`) and re-included below; no relevant commits have landed in the 2h gap.

# Findings

## 1. `internal/agent/codex.go` puts the full prompt on `argv` (new — process-table leak)

- **Location**: `internal/agent/codex.go:83-84` (build of `args`), `internal/agent/codex.go:95` (`exec.CommandContext(ctx, command, args...)`). Compare with `internal/agent/claude.go:106` (`cmd.Stdin = strings.NewReader(req.Prompt)`).
- **Severity**: MEDIUM
- **Effort**: SMALL

- **Description**: The codex run path constructs argv as `codex exec --json <flags...> <prompt>` — the entire `req.Prompt` body is the final positional argument. From the moment `cmd.Start()` returns (line 117) until codex exits, `ps auxww`, `/proc/<pid>/cmdline`, audit-log argv captures, and any supervisor that snapshots child processes (launchd, supervisord, systemd journal) see the complete prompt. Prompts assembled for ateam runs include the full role base prompt, the merged project + role prompt, any prior-report context, and (depending on the agent) the contents of `secrets.env` references the prompt enumerates by name. In practice prompts also frequently include verbatim file excerpts (review/security reports cite source code paths and lines, sometimes including connection strings or tokens checked into the codebase). The trigger is unavoidable: every single codex invocation (review, report, code, verify) leaks. The class is identical to the one the codebase already mitigated for `docker -e KEY=VALUE` (`internal/container/docker.go:376-398`) and for the claude prompt (`internal/agent/claude.go:106`). The asymmetry is explicit — the docstring at `internal/agent/codex.go:24` says "The prompt is passed as a positional argument, not stdin." — but the security tradeoff was not stated. Codex `exec` supports reading the prompt from stdin by passing `-` as the positional argument (the same convention as most CLI tools that have a positional input); verify against the installed codex version before switching.

- **Recommendation**: Switch the codex `Run` path to either (a) pass `-` as the positional prompt argument and write `req.Prompt` to `cmd.Stdin`, mirroring `claude.go`, or (b) if codex `exec` rejects `-` on the supported versions, write the prompt to a 0600 tempfile under `os.TempDir()` and pass the path positionally (cleanup on `cmd.Wait`). Confirm by running `ateam call --agent codex …` then `ps auxww | grep codex` from a second shell during the run — the prompt content must not appear. Update the `internal/agent/codex.go:24` docstring to record the new behavior and the reason. Also worth noting: the `DebugCommandArgs` helper (`internal/agent/codex.go:61-68`) intentionally does not include the prompt, so debug logging would not need to change.

## 2. `ateam secret --value <SECRET>` leaks the secret value via the process table (re-included from prior report — unresolved)

- **Location**: `cmd/secret.go:56` (flag definition) and `cmd/secret.go:179-189` (consumed by `setSecret`).
- **Severity**: MEDIUM
- **Effort**: SMALL

- **Description**: The `secret` subcommand defines a `--value` flag bound to `secretValue`. When a user runs `ateam secret SOME_KEY --set --value sk-xxxx`, the secret value sits in process arguments and is visible to any local user via `ps auxww`, in shell history, in audit logs, and in supervisord/launchd argv captures. The default code path (stdin / interactive paste) is fine, and the `Long` help in `cmd/secret.go:32-46` correctly demonstrates the stdin form; the `--value` flag is registered without any usage warning and is not mentioned in `Long`. The trigger is the explicit user choice of `--value`, but the failure mode — secret reaches argv — is the exact class of leak the rest of the codebase carefully avoids (see `internal/container/docker.go:376-398` and `internal/container/container.go:120-149`, where `-e KEY=VALUE` was deliberately split into `-e KEY` argv plus `cmd.Env` value for this reason). State as of this cycle: unchanged from prior report — `cmd/secret.go` has not been modified.

- **Recommendation**: Remove the `--value` flag, or replace it with `--value-from-env <ENVVAR>` (the secret stays in the env, never in argv); alternately, replace `--value` with `--from-file <path>` so scripted callers can pipe via a 0600 tempfile. If the flag must stay for backward compatibility, print a one-line warning to stderr whenever `--value` is non-empty and document the risk in `Long`. The `--get` and `--print` paths (`cmd/secret.go:147,268`) are not findings — they emit values to stdout under explicit user request and rely on the caller's redirection.

## 3. CSP allows `'unsafe-inline'` for `script-src` and `style-src` — defense-in-depth, flagged for visibility only (re-included from prior report — unresolved)

- **Location**: `internal/web/server.go:291-298`.
- **Severity**: LOW
- **Effort**: MEDIUM

- **Description**: `securityHeaders` sets `script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'`. This is defense-in-depth, not an active vulnerability: goldmark is constructed without `html.WithUnsafe()` (see `internal/web/markdown.go:67-73`), so user-supplied markdown cannot inject raw `<script>` tags, and the server is bound to `127.0.0.1` by default (`cmd/serve.go:69-78`) with an explicit "accessible without authentication" warning when `--public` or `--bind` is used. Tightening to nonces would require verifying every embedded template, the Chroma syntax-highlighter output, and the export HTML still render correctly without inline styles/scripts. Filed for visibility only — per the role's hard rule on CSP changes, this is flagged for human review rather than an actionable recommendation, because (a) I cannot prove the templates do not rely on inline JS/CSS without running the UI, (b) there is no fallback plan if a tightened CSP breaks the dashboard, and (c) the localhost default + warning is the compensating control. State as of this cycle: unchanged.

- **Recommendation**: No action without explicit owner sign-off. If revisited later, the verification step is "load `ateam serve`, click through every nav tab (overview, reports, review, verify, runs, prompts, cost, sessions, code), confirm no console CSP violations and no visual regressions in Chroma-highlighted code blocks." The fallback is to revert the header to the current value.

## 4. `dirName` URL segment not filtered for `..` in code-session handlers — bounded but inconsistent (re-included from prior report — unresolved)

- **Location**: `internal/web/handlers.go:1301-1371` (`handleCodeSessionDetail`) and `internal/web/handlers.go:1373-1427` (`handleCodeSessionFile`), via `codeSessionDirs` at `internal/web/handlers.go:1176-1182`.
- **Severity**: LOW
- **Effort**: SMALL

- **Description**: The `{session}` URL path value is used directly in `filepath.Join(projectDir, "supervisor", "code", dirName)`. `fileName` is properly rejected when it contains `..` or `/` (handlers.go:1387), but `dirName` is not. The final `isPathWithin(absPath, pe.ProjectDir)` check at line 1397 contains any traversal to within `projectDir` (i.e., the `.ateam/` directory). Since the entire `.ateam/` tree is already exposed by design through the other read-only handlers, there is no real escalation — a caller cannot read files outside `.ateam/` and is further constrained to `.md` files. The defect is scope creep (a `/p/<slug>/code/../<file>.md` URL can resolve to `.ateam/<file>.md` which is not a code-session file) rather than a confidentiality breach. The web server is also localhost-only by default with an explicit network-exposure warning. State as of this cycle: unchanged.

- **Recommendation**: Reject `dirName` values containing `..`, `/`, or starting with `.` at the top of both handlers, mirroring the existing `fileName` guard. This is consistency, not exploit prevention.

# Quick Wins

1. **Finding #1** (MEDIUM, SMALL): switch codex prompt delivery from positional argv to stdin (`-` positional + `cmd.Stdin`) to close the `ps aux` leak. Symmetry with `claude.go:106` and the documented argv-vs-env split in `docker.go:376-398`.
2. **Finding #2** (MEDIUM, SMALL): drop the `ateam secret --value` flag or rename to `--value-from-env`; if kept, warn on stderr and document the leak in `Long`.
3. **Finding #4** (LOW, SMALL): add a `..`/`/` reject on `dirName` in `handleCodeSessionDetail` / `handleCodeSessionFile` to match the existing `fileName` guard.

Three quick wins — no padding with defense-in-depth.

# Project Context

- **Codebase type**: Go CLI (`main.go`, `cmd/`, `internal/`) plus an embedded read-only web UI for browsing run artefacts. Built/tested via `make build` / `make test` / `make test-docker`. HEAD at audit time: `6dcf9a0`.
- **Primary security boundaries**:
  - Secret storage: `internal/secret/store.go` (FileStore at 0600, parent dir 0700; keychain via `github.com/zalando/go-keyring`); resolver chain project → org → global → env in `internal/secret/resolve.go`.
  - Process-table hygiene for secrets in containers: `internal/container/container.go:120-149` (`stagedEnv`) and `internal/container/docker.go:376-398` / `internal/container/docker_exec.go:285-305` (`envArgs`) — secret values reach docker via `cmd.Env`, only `-e KEY` (no value) on argv. Documented at `internal/container/docker.go:376-380`.
  - Process-table hygiene for prompts: `internal/agent/claude.go:106` uses `cmd.Stdin = strings.NewReader(req.Prompt)`. **Codex parity is broken** — see Finding #1: `internal/agent/codex.go:83-84` passes the prompt positionally.
  - Web UI: localhost by default in `cmd/serve.go:69-78`; CSP, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` in `internal/web/server.go:291-298`; path-confinement via `isPathWithin` in `internal/web/handlers.go:651-656`; safe-by-default goldmark (no `WithUnsafe`) in `internal/web/markdown.go:67-73`.
  - SQLite state: `internal/calldb/calldb.go:122-129` pre-creates the DB at 0600; `internal/calldb/queries.go:327-374` uses parameterized `?` placeholders even in dynamic `IN (?,?,…)` lists — no string-interpolated SQL.
  - Subprocess invocation: `git`, `docker`, `cp`, `claude`, `codex`, `security` all called via `exec.Command(name, args...)` with fixed argv. Shell helpers in `cmd/agent_config.go:730,760` correctly use `sh -c '<script>' "_" "$@"` positional passing (no interpolation of untrusted strings into the script body).
- **Notable existing security work (do not re-flag)**:
  - `internal/container/docker.go:376-380` comment explicitly documents the argv-vs-env split for secrets.
  - `internal/agent/claude_auth.go:289-321` (`BuildCleanEnv`) strips conflicting auth env vars; `internal/secret/validate.go:97-153` (`IsolateCredentials`) prevents credential confusion at the agent layer.
  - `cmd/serve.go:76-78` warns when binding off-localhost — the read-only-without-auth localhost default is a documented choice; do not propose adding auth in future cycles without owner confirmation that remote usage is a goal.
- **Files / dirs to revisit in future cycles**:
  - `internal/agent/codex.go` for resolution of finding #1 (verify against installed codex version that `-` is accepted as the exec positional).
  - `cmd/secret.go` for finding #2.
  - `internal/web/handlers.go` code-session handlers for finding #4.
  - Auto-setup / code / review / code-session flows when new HTTP routes appear under `internal/web/server.go:240-258`.
  - Any future agent added alongside Claude/Codex — assert the new agent uses stdin for the prompt (or a tempfile) at PR review time.
- **Out of scope for this role**: dependency CVEs (handled by `project.dependencies`); coverage gaps; feature/design recommendations.
