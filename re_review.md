We just did a round of code based on the current report. These are not new reports. But because there were so many findings let's do another round of review: select 5 high value tasks. We want to specifically focus on:
* improve code structure and readability (like better interface design, less redundant code), possibly more complex code restructure is ok for this round
* improve test coverage

Do note that some code features have made it since the last reports but most of the reports is probably still accurate.

Here is my critical and important feedback about deferred items:

Deferred
* Module path rename (github.com/ateam → final public path): Valid and blocking for public release, but requires a product decision on the canonical URL. Mechanical to execute once decided. (critic_engineering, basic_project_structure)
  * feedback: do note that the module path on github is https://github.com/voidexpr/ateam at the moment
* Container interface redesign (extend with lifecycle methods to eliminate type assertions): The right long-term fix for the growing type-assertion chains in runner.go, but LARGE effort. Worth doing when a 6th container type is considered. (refactor_architecture, critic_engineering)
  * feedback: do it !! We can also get rid of these containers: devcontainer.go, docker_sandbox.go, remove support for persistent containers (update docs too). Only keep one-shot docker and docker exec
* CI/CD pipeline: Multiple roles flag this. Per project instructions, CI/CD and GitHub Actions are explicitly out of scope. Pre-commit hooks (Action 9) address the immediate enforcement gap locally.
  * feedback: yes let's focus on tests themselves and less about CI/CD. For the time being let's have a 'test-all' makefile target in place of CI/CD
* SQLite schema versioning: The archaeological migration approach works today. Worth adopting a version table when the next schema change arrives, not as a standalone refactor. (database_schema, critic_engineering)
  * feedback: old migrate code isn't needed at all anymore but there might be migrations in the future
* TOML/HCL/JSON config unification: Three parsers is defensible given HCL's strength for the block-based runtime config. The inline JSON sandbox in runtime.hcl is the real pain point — consider extracting it to a Go-generated JSON. (critic_engineering)
  * feedback: this is completely invalid: We want to keep this config files as-is, this is a product feature decision. You and the report roles are focused on not changing features, just improve quality. Point taken that multiple config formats isn't great but this saves tons of code to make the system very flexible.
* Third-party notice inventory (MPL-2.0 for hcl/v2): Required before public release. Generate with go-licenses. (dependencies)
  * feedback: let's wait
* Replace goldmark-highlighting/v2 with direct chroma integration: Stale pseudo-version, but the highlighting works. Do when upgrading goldmark to v1.8.x. (dependencies)
  * feedback: let's wait
* Web server HTTP timeouts and graceful shutdown: The server is localhost-only and GET-only. Worth adding, but low blast radius. (production_ready)
  * feedback: let's wait
* ateam run default timeout: Missing config section for run timeout. Worth adding but not blocking — interactive users can Ctrl+C. (production_ready)
  * feedback: let's wait
* Secret backend strict mode (--storage file still falls back to keychain): Requires a product decision on whether cross-backend fallback is a feature or a bug. (production_ready)
* extra_volumes host-path containment: Valid security concern but requires deciding the allowlist policy. A cloned repo with malicious runtime.hcl could mount arbitrary paths. (security)
  * feedback: let's wait, not sure we need to address it yet
* Secret values in process argv: All container backends forward secrets as -e KEY=VALUE. The --env-file approach is the right fix but touches 4 files across 4 backends. (security)
  * feedback: let's keep it around but let's do it later
* Pool orchestration duplication between cmd/report.go and cmd/parallel.go: ~80 lines of near-identical channel wiring. Extract when either changes. (refactor_architecture)
  * feedback: high priority, the code was supposed to be reused
* Runner.Run 400-line method: Needs decomposition, but tightly coupled to the container type-assertion issue. Address together. (critic_engineering)
  * feedback: important type of change for this round
* Custom role docs mismatch (REFERENCE.md says excluded from all, code includes them): Docs fix, but the behavior itself may be intentional — needs a product decision on whether custom roles should default to on or off in --roles all. (docs_external)
  * feedback: high priority ! custom roles are included in 'all' if they are 'on', otherwise they can always be run by being name explicitly. Docs and code must match.
* migrate-logs drops cache_write_tokens and output_file: Data-loss gap during org-to-project migration. Important to fix before the next migration wave. (database_schema)
  * feedback: we don't need to do any org to project migration at the moment, all installs were migrated.
* Sandbox policy dual sources (runtime.hcl vs standalone JSON): The standalone file is decorative — either generate it from runtime.hcl or stop deploying it. (critic_engineering)
  * feedback: let's wait on that, I think I still need it for some testing but let's keep this finding around for later
* Pinning Claude Code version in Dockerfile: Non-deterministic builds from npm install -g @anthropic-ai/claude-code. Pin when next updating the image. (production_ready)
  * feedback: up to you
* Agent process boilerplate deduplication (claude.go/codex.go share ~80 lines): Worth extracting a shared agentRunner, but wait until codex agent is more actively used. (shortcut_taker, refactor_small)
  * feedback: up to you, I like refactoring
* DEV.md container table and mount permissions: Lists only 2 of 5 container types with inverted mount permissions. Fix alongside any container documentation pass. (docs_internal)
  * feedback: high priority, bad docs is dangerous
* .ateam/overview.md stale references: Wrong supervisor filename, missing packages. This file is injected into every agent prompt. (docs_internal)
  * feedback: high priority product decision to
* poc-shell/ and plans/ cleanup: Historical material at repo root. Low priority cosmetic cleanup. (basic_project_structure)
  * feedback: yes, let's wait
* Timestamp storage format (local RFC3339 vs UTC): Lexicographic sorting breaks across DST transitions. Fix in the next schema migration. (database_schema)
  * feedback: ok

Conflicts
* idle_timeout removal vs future use: shortcut_taker says remove dead config; the field exists for future persistent container eviction. Resolution: Defer removal — it's dead config but causes no harm, and removing it would break any runtime.hcl files that already set it.
  * feedback: we can get rid of it

Notes
* The container subsystem is the fastest-growing area of the codebase (now 5 backends across 4 files) but also the most architecturally stressed. Every new container type adds type assertions in 3+ files. The current approach works but the maintenance cost per new backend is high.
  * feedback: propos

* Test coverage is strong in the data layer (calldb 83%, runtime 77%, config 74%) but thin at the boundaries: cmd 19.7%, web 15.5%, agent 28.2%. The no-auth mock agent and test profile provide the infrastructure needed for cmd-level smoke tests — it's a matter of writing them.
  * feedback: let's do that !! time to add tests

* The competitive landscape (Claude Code /schedule, Cursor Automations) is converging on ateam's space. The project's differentiation is the multi-role pipeline, structured artifacts, and cost tracking — none of which the competitors offer. Positioning documentation and the module path rename become more urgent as the window narrows.
  * feedback: could add a FAQ entry at some time. Having flexible execution engines (for example review with codex, code with claude or work with docker) is also important to run tests for some projects. Making ateam changes safer
