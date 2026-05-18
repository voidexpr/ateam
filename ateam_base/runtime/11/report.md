# Summary

The ateam codebase shows generally careful security hygiene: SQL is parameterized through `database/sql`, secrets files are written `0600`, the web UI escapes markdown/HTML output, path joins are validated with `isPathWithin`, and the local web server defaults to `127.0.0.1`. The most material exposure is the opt-in `ateam serve --public/--bind` mode, which publishes every report, stream log and prompt on the LAN with no authentication beyond a stderr warning; a handful of smaller, lower-impact issues (symlink-blind path containment, world-readable `.ateam/` subtrees, no checksum on the Go tarball download) round out the findings. No critical injection, secrets-in-code, or auth-bypass issues were found in the audited paths.

Role: security · Model: claude-opus-4-7 · Extended thinking not enabled.

# Findings

## 1. `ateam serve --public/--bind` exposes the full project state with no authentication

- **Location**: `cmd/serve.go:69-80`, `internal/web/server.go:230-298`
- **Severity**: MEDIUM
- **Effort**: MEDIUM
- **Description**: When the user passes `--public` (bind `0.0.0.0`) or `--bind <ip>`, the server listens on a non-loopback interface but does not enforce any authentication, TLS, or rate limiting. Only a stderr warning is emitted (`cmd/serve.go:77`). Any host on the same L2/LAN can then read:
  - All `report.md`, `review.md`, `verify.md` files (which often quote source code snippets, file paths, and finding details).
  - Full agent stream logs via `/p/<slug>/runs/{id}/logs` — includes the rendered prompt sent to the LLM, tool inputs, tool outputs, and assistant text. Prompts routinely contain repository contents the agent inspected.
  - The on-disk `prompt.md`, `cmd.md`, and `stderr.out` for every historical run.
  
  These artifacts can leak: source code, internal URLs, credentials accidentally pasted into prompts/tools, container names, environment variable *names* (forward_env list), and architectural details. The CSP/headers (`securityHeaders`, `internal/web/server.go:291`) protect the browser but do nothing for unauthenticated network access.
- **Recommendation**: Require an auth token (e.g., generated at startup and printed once on stderr, then enforced via `Authorization` header or a `?token=` query) whenever the bind address is not loopback; or refuse to start with `--public` unless `--auth-token` or `--no-auth` is also passed. At minimum, document the exposure prominently in `README.md`/`docs/` and tighten the warning to list the categories of data exposed.

## 2. `isPathWithin` is string-based and does not resolve symlinks

- **Location**: `internal/web/handlers.go:651-656`, exercised by `handleRunFile`, `handleCodeSessionFile`, `serveHistoryFile`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Containment is checked with `strings.HasPrefix(filepath.Clean(absPath), filepath.Clean(baseDir)+sep)`. If any file or directory under `.ateam/` is a symlink that escapes the project root (e.g., a symlinked `logs/` directory pointing at the user's home, or an attacker-created symlink in a writable subdirectory of a shared `.ateamorg`), the web server will follow the symlink and serve content outside `ProjectDir`/`OrgDir`. Locally this only matters if the project directory is writable by an untrusted process; combined with finding #1 it becomes serious if the server is `--public`.
- **Recommendation**: After computing `absPath`, run `filepath.EvalSymlinks` and re-apply `isPathWithin` against the resolved path (and also `EvalSymlinks(baseDir)` for consistency). Reject the request if `EvalSymlinks` fails for an existing file.

## 3. `.ateam/` role/history/log directories created world-readable (`0755`)

- **Location**: `internal/root/init.go:37, 43, 97, 103, 108, 167, 170, 180, 184`; `internal/web/handlers_test.go` uses 0644 for fixture files but matching production paths follow the same pattern
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `InitProject` and `EnsureRoles` create `.ateam/roles/<id>/history/`, `.ateam/supervisor/`, `.ateam/logs/...`, etc. with `0755`. The `report.md` / `review.md` outputs land under these directories. On shared multi-user systems (CI runners, dev jumphosts) any other local user can list and read all reports, agent prompts archived in `history/`, and the `cmd.md`/`prompt.md` files under `logs/<exec_id>/`. Note: `secrets.env` and SQLite are written `0600`, so explicit secrets are protected — the leak is metadata + agent-visible source excerpts.
- **Recommendation**: Switch new-directory permissions to `0700` (matches the existing `0700` already used in `internal/runner/runner.go:305, 309` for log dirs) and rely on `umask` for legacy paths. If interop with multi-user team setups is desired, add a `permissions` setting in `config.toml`.

## 4. Go tarball downloaded without checksum verification

- **Location**: `install.sh:54-60`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: `curl -fsSL "$url" -o "/tmp/$tarball"` then `sudo tar -C /usr/local -xzf "/tmp/$tarball"`. Trust is entirely in TLS to `go.dev`; no SHA-256 verification against `go.dev/dl/?mode=json`. A TLS-MITM or compromised mirror could swap in a tampered Go toolchain that subsequently builds the `ateam` binary with root privileges (the script uses `sudo`).
- **Recommendation**: Fetch `https://go.dev/dl/?mode=json` (or hard-code the published SHA-256 next to `REQUIRED_GO_VERSION`) and verify with `sha256sum -c` before extraction.

## 5. Container/agent names from the secret store are not validated for shape

- **Location**: `internal/container/docker_exec.go:226-249, 156-163` (`ResolveRunningContainerName`, `dockerCp` callers); resolution path: `cmd/agent_config.go` → `secret.Resolve("CONTAINER_NAME")`
- **Severity**: LOW
- **Effort**: SMALL
- **Description**: Container names sourced from `ateam secret` or env are passed as positional argv to `docker exec/cp`. There is no shell injection (`exec.Command` is used everywhere, not `sh -c "$name"`), but a value beginning with `--` or `-` could be interpreted as a flag by `docker` (`docker exec --rm-volumes ...`). This is self-attack territory (the user controls their own secret store), but a stronger validator prevents accidental footguns when CI templates render unexpected values.
- **Recommendation**: Reject container names containing characters outside `[A-Za-z0-9_.-]` or starting with `-` in `ResolveRunningContainerName` and at the secret-store write boundary.

## 6. Inline `<script>` block forces `script-src 'unsafe-inline'`

- **Location**: `internal/web/server.go:293`, `internal/web/templates/layout.html:36-64`
- **Severity**: LOW (informational — do **not** tighten without verifying the page still works)
- **Effort**: MEDIUM
- **Description**: The CSP grants `'unsafe-inline'` to both `script-src` and `style-src` because `layout.html` ships an inline `<script>` for click-row navigation + table sorting. The agent-generated markdown is rendered via goldmark which (a) escapes raw HTML, (b) sanitizes `javascript:`/`data:` URLs in links, and (c) routes code blocks through chroma which writes only escaped output — so the inline-script permission isn't currently exploited by content. Flagging only so this remains a deliberate choice if the inline block is ever extracted to `/static/`.
- **Recommendation**: If/when the inline `<script>` is moved into `static/app.js`, drop `'unsafe-inline'` from `script-src`. Per the role guidance, do not change CSP without runtime verification.

# Quick Wins

1. **Add auth or refuse to start when `--public`/non-loopback bind is used** (Finding #1) — single biggest exposure, ~few-hours work to add a generated token + Bearer-check middleware.
2. **Tighten new-directory perms to `0700` in `internal/root/init.go`** (Finding #3) — one-line changes to six `MkdirAll` calls; matches the perms already in `internal/runner/runner.go`.
3. **EvalSymlinks-then-recheck in `isPathWithin` callers** (Finding #2) — small refactor of the three web handlers, removes a class of "symlink escape" surprises especially relevant if #1 is enabled.
4. **Verify the Go tarball SHA-256 in `install.sh`** (Finding #4) — short shell change against `go.dev/dl/?mode=json`.

# Project Context

- **Stack**: Go 1.26 CLI (`module github.com/ateam`). Single binary `ateam`. Direct dependencies: `cobra`, `BurntSushi/toml`, `hashicorp/hcl/v2`, `yuin/goldmark`, `alecthomas/chroma/v2`, `modernc.org/sqlite` (pure-Go SQLite, no CGO), `zalando/go-keyring`, `vbauerster/mpb` (TUI), `mattn/go-isatty`.
- **Networked surface**: the embedded web server in `internal/web/`. Routes registered in `internal/web/server.go:240-258`. Read-only HTTP; no POST handlers, no auth layer. Bind controlled by `cmd/serve.go` (`--public`, `--bind`).
- **Auth/secrets**: `internal/secret/` (resolver: project → org → global → env; backends: OS keychain via `go-keyring`, file `secrets.env` with `0600`). Claude credentials handled in `internal/agent/claude_auth.go`.
- **Subprocess execution**: `os/exec` only — no `sh -c` invocation of user-controlled strings in production code (`/bin/sh -c` appears only in `*_test.go` and `cmd/agent_config.go` with fixed format strings using `$1` positional args). Notable callers: `internal/container/docker.go`, `internal/container/docker_exec.go`, `internal/agent/claude.go`, `internal/agent/codex.go`, `internal/agent/claude_auth.go`, `cmd/agent_config.go`, `internal/eval/worktree.go`, `internal/gitutil/gitutil.go`, `internal/fsclone/clone.go`.
- **SQL**: `internal/calldb/{calldb,queries}.go` — every statement uses `?` placeholders.
- **Markdown rendering**: `internal/web/markdown.go` — goldmark default renderer (raw HTML escaped, dangerous-URL sanitization on by default) plus chroma syntax highlighter.
- **Path containment helper**: `internal/web/handlers.go:651-656` `isPathWithin` — string-based, no symlink resolution. Tests in `internal/web/is_path_within_test.go`.
- **Init permissions**: `internal/root/init.go` (`0755` dirs), `internal/runner/runner.go` (`0700` log dirs, `0600` files), `internal/secret/store.go` (`0600` files, `0700` dirs).
- **CSP location**: `internal/web/server.go:291-298` `securityHeaders` middleware. Inline script in `internal/web/templates/layout.html:36-64`.
- **Install script**: `install.sh` — downloads Go tarball via curl, no checksum verification.
- **Out-of-scope by role guidance**: dependency CVE chasing (the dependency role owns this), tightening CSP/CORS without runtime verification.
- **No prior security report**: `.ateam/roles/security/history/` and `.ateam/roles/project.security/history/` are empty; this is a baseline pass.
