# ATeam — Project Overview

## Project Goals

ATeam is a CLI tool that orchestrates role-specific AI coding agents (Claude Code, Codex) to
continuously audit and improve software quality across a codebase — without ever changing
features. It is designed to run unattended (nightly, scheduled, or on-demand) and produce
transparent, auditable markdown artifacts at every step.

Key principles: no feature work, unattended execution, pragmatic prioritization, safe sandboxed
execution, generally applicable across tech stacks, and auditable cost tracking.

This project is **self-hosted**: ateam runs on its own codebase.

## Tech Stack

- **Language:** Go 1.25
- **CLI framework:** cobra + pflag
- **Configuration:** TOML (project config), HCL v2 (runtime/agent/profile config)
- **Database:** SQLite via modernc.org/sqlite (embedded, for cost/run tracking)
- **Markdown:** goldmark + chroma (for rendering reports in web UI)
- **Agents supported:** Claude Code (primary), OpenAI Codex (partial)
- **Containerization:** Docker 29.2.0 (installed on this host)

## Main Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/BurntSushi/toml` | Config parsing (config.toml) |
| `github.com/hashicorp/hcl/v2` | Runtime config parsing (runtime.hcl) |
| `modernc.org/sqlite` | Embedded SQLite for cost/run DB |
| `github.com/yuin/goldmark` | Markdown rendering (web UI) |
| `github.com/zclconf/go-cty` | HCL type system |
| `github.com/mattn/go-isatty` | Terminal detection |
| `github.com/google/uuid` | Run ID generation |
| `github.com/dustin/go-humanize` | Human-readable output formatting |

## Main Folders and Files

```
ateam/
├── main.go                  # entry point
├── Makefile                 # build, test, vuln targets
├── go.mod / go.sum          # Go module
├── cmd/                     # CLI commands: report, review, code, all, cost, ps, serve,
│                            #   tail, init, roles, env, secret, run, runs, install, ...
├── internal/
│   ├── agent/               # agent abstraction (claude, codex, mock)
│   ├── calldb/              # SQLite cost/run tracking database + schema
│   ├── config/              # config.toml loading and resolution
│   ├── container/           # Docker container management
│   ├── display/             # output formatting helpers
│   ├── gitutil/             # Git helpers (worktree, commit, etc.)
│   ├── prompts/             # Prompt file resolution (3-level fallback)
│   ├── root/                # Project/org root discovery
│   ├── runner/              # Run orchestration (report, review, code pipeline)
│   ├── runtime/             # runtime.hcl parsing (agents, profiles, containers)
│   ├── secret/              # Secret management (store, resolve, validate)
│   ├── streamutil/          # stream event parsing (JSONL, BOM handling)
│   └── web/                 # Web UI server (ateam serve)
├── defaults/
│   ├── roles/               # 17 built-in role prompt files
│   ├── supervisor/          # Supervisor prompt files (incl. auto_setup_prompt.md)
│   ├── runtime.hcl          # Default agent/profile/container definitions
│   ├── config.toml          # Default config template
│   └── Dockerfile           # Docker image for containerized agent runs
├── .ateam/                  # Ateam's own project config (self-hosted)
├── test/                    # Docker-in-Docker integration test setup
├── test_data/               # Test fixtures
├── plans/                   # Design and planning documents
└── poc/                     # Proof of concept / historical experiments
```

## Project Maturity

- Medium-sized Go codebase (~19K lines across cmd/ and internal/)
- Actively developed, approaching public release (has install.sh, README, docs)
- Self-hosted: ateam runs on its own codebase
- No CI/CD pipeline (intentional, local-first development)
- Docker 29.2.0 is installed on this host

## Docker Status

Docker **is installed** (v29.2.0) on this host. Docker-based profiles and test targets are
available:

- `docker` profile (oneshot) and `docker-exec` container type in runtime.hcl
- `make test-docker` — Docker-in-Docker integration tests (no API key needed)
- `make test-docker-live` — live agent tests (requires ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN)

Note: `make test-docker` runs DinD and requires the Docker socket. It builds a linux/amd64 binary
internally — cross-compilation via Go toolchain (no pre-built binary needed).

## Ateam Configuration Recommendations

### Roles

All currently enabled roles are appropriate for this project:

- **automation** `on` — Makefile exists; linter/test automation worth improving
- **basic_project_structure** `on` — one-time structural audit for a medium Go project
- **critic_engineering** `on` — meta-check of ateam's own engineering quality
- **critic_project** `on` — strategic review as project approaches public release
- **database_config** `off` — SQLite is embedded; no connection pool/server config to tune
- **database_schema** `on` — SQLite schema in internal/calldb worth reviewing
- **dependencies** `on` — Go module dependency health
- **docs_external** `on` — README and install docs need to be accurate for public release
- **docs_internal** `on` — internal package docs and inline comments
- **production_ready** `on` — approaching public release, production readiness matters
- **project_characteristics** `off` — one-time profiling role, not useful for ongoing runs
- **refactor_architecture** `on` — medium-sized codebase with room for structural improvement
- **refactor_small** `on` — ongoing small refactors
- **security** `on` — CLI tool handling API keys and secrets, worth auditing
- **shortcut_taker** `on` — agents could take shortcuts; worth checking
- **testing_basic** `on` — baseline unit test coverage
- **testing_full** `on` — project is mature enough; Docker tests are now available

### Code Profile

Use `cheap` (Sonnet at $0.50 budget) for this medium-sized Go codebase. Upgrade to `default`
if code quality work needs more depth or if the codebase grows significantly.

### How to Run Tests

**Quick tests** (for simple/focused changes):
```bash
make test
```
Runs `go test ./...` across all packages. Fast, no Docker required.

**Full test suite** (for agent, container, runner, or Docker-related changes):
```bash
make test && make test-docker
```
`make test-docker` runs Docker-in-Docker integration tests. Required any time `internal/agent/`,
`internal/container/`, `internal/runner/`, or `test/` files are modified.
