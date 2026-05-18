### Project Assessment

The documentation set is in good shape overall — README, COMMANDS, CONFIG, ISOLATION, CONCURRENCY, EVAL, FAQ, and ROLES are all populated, cross-linked, and the Go side has package doc comments throughout. The remaining issues are concentrated and mostly factual: a wrong `git clone` URL in the Quick Start, a doc/code Go-version drift, an `APPROACH.md` stub linked as canonical rationale, a `Tips and Tricks` section that ships as a literal TODO list, and a `ROLES.md` that exposes a `.` vs `_` naming duplication with no explanation. These are high-leverage, low-effort fixes that would materially improve first-contact experience for both users and contributors.

### Priority Actions

**1. Fix the broken `git clone` URL and copy-paste typos in the README Quick Start**
- **Action**: In `README.md`, (a) replace `https://github.com/voidexpr/ateam.git` at lines 55 and 107 with the canonical repo URL (or an obvious `<org>` placeholder + one line on building from a local checkout if no public repo exists yet), (b) fix `ataem serve` → `ateam serve` at line 78 (this sits inside a copy-paste code block and will fail with "command not found"), and (c) fix `cost transaprency` → `cost transparency` at line 28.
- **Source Role**: docs_external (2026-05-14_13-13-11)
- **Source Report**: .ateam/roles/docs_external/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: The first thing a new user copy-pastes from the README currently does not work. Highest-impact, lowest-effort fix in the whole review.

**2. Reconcile the Go version across `go.mod`, `README.md`, and `install.sh`**
- **Action**: Decide the real minimum Go version, then make all three sources agree. Current state: `go.mod:3` declares `go 1.26.3`, `Makefile` pins `golang:1.26` for the `companion-race` target, but `README.md:99`, `README.md:106`, `DEV.md:11`, and `install.sh:6` (`REQUIRED_GO_VERSION="1.25"`) all advertise `Go 1.25+`. If 1.26 is the real floor, update README (two lines), DEV.md, and `install.sh` (`REQUIRED_GO_VERSION` and the `install_go` Linux tarball name). If 1.25 is acceptable, relax `go.mod`.
- **Source Role**: docs_external (2026-05-14_13-13-11), docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_external/report.md, .ateam/roles/docs_internal/report.md
- **Priority**: P0
- **Effort**: SMALL
- **Rationale**: A developer with Go 1.25 currently passes `install.sh`'s version check, then fails confusingly at `go build`. Both reports flagged independently.

**3. Resolve `APPROACH.md` — either populate it or stop linking to it as canonical**
- **Action**: `APPROACH.md` is a 3-line stub but is cross-linked from `README.md:13` ("rationale and design principles") and `README.md:374` ("More docs"). Pick one: (a) move the existing "Why ATeam" + "Core principles" prose out of `README.md` into `APPROACH.md`, leaving the README with a one-paragraph summary + link; or (b) delete `APPROACH.md` and remove both inbound links, inlining the rationale in the README. Do not keep a stub referenced as authoritative.
- **Source Role**: docs_external (2026-05-14_13-13-11), docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_external/report.md, .ateam/roles/docs_internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: Two independent reports flagged the same dead-end. The current state is the worst of both options.

**4. Replace the README `Tips and Tricks` TODO list with real content or remove the section**
- **Action**: `README.md:330-338` ships a `## Tips and Tricks` header followed by literal `TODO:` and six unwritten bullets. Either (a) replace each bullet with a one-line description plus a link to the relevant section in `ISOLATION.md` / `FAQ.md` / `COMMANDS.md` (several topics — separate agent config, worktree, scripted parallel runs — are already covered there), or (b) remove the section until content exists. Do not leave the literal word `TODO:` on a user-facing landing page.
- **Source Role**: docs_external (2026-05-14_13-13-11), docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_external/report.md, .ateam/roles/docs_internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: A published heading with no body is a poor first impression and signals an unfinished project. Both reports flagged it.

**5. Add a naming-convention preamble to `ROLES.md` and mark legacy/dotted relationships**
- **Action**: `ROLES.md` lists ~40 roles where many appear in dual `_` vs `.` form (`docs_external` / `docs.external`, `critic_engineering` / `critic.engineering`, `database_schema` / `database.schema`, `testing_basic` vs `test.gaps`/`test.quality`/`test.recent`, etc.) with no explanation of which to prefer. Emit a one-paragraph preamble from `cmd/roles.go` when `--docs` is passed that documents the convention (e.g., "Dotted names are the newer finer-grained roles; underscore names are the original coarser roles kept for compatibility — prefer dotted for new projects"), and mark deprecated entries with `(legacy, prefer X)` in the Description column. Update README's default-roles table at `README.md:269-281` to note the dotted alternatives exist.
- **Source Role**: docs_external (2026-05-14_13-13-11), docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_external/report.md, .ateam/roles/docs_internal/report.md
- **Priority**: P1
- **Effort**: SMALL
- **Rationale**: New users picking roles for `auto-setup` or `config.toml` have no way to know which family to enable. Auto-generation means the fix lives in `cmd/roles.go` and updates `ROLES.md` on the next `make docs`.

**6. Move the README `Future` roadmap section to `ROADMAP.md`**
- **Action**: `README.md:344-366` carries 20+ lines of unreleased plans ("0.9.0 Refactor roles…", "Memory system: persist preferences…", more agents). Move to a new `ROADMAP.md` and replace in the README with a single line: "See ROADMAP.md for what's coming next."
- **Source Role**: docs_external (2026-05-14_13-13-11)
- **Source Report**: .ateam/roles/docs_external/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Roadmap on the landing page bloats README, dates poorly, and blurs what ateam does today vs. tomorrow.

**7. De-duplicate `AGENTS.md` and `CLAUDE.md`**
- **Action**: The two files are byte-identical (14 lines each) with no shared-source mechanism. Make one canonical (e.g., `AGENTS.md`) and replace the other's body with `See [AGENTS.md](AGENTS.md).`. If both must literally exist for tool discovery reasons, add a top-of-file note in each declaring which is canonical and that the other is a mirror.
- **Source Role**: docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_internal/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: Silent-drift hazard — a future edit to one will mislead one class of agent.

**8. Mark `docs/plans/` files as historical**
- **Action**: `docs/plans/2026-03-07-org-project-split-design.md` and `…-plan.md` describe the `.ateamorg`+`.ateam` split as a future task, but the split is fully shipped (visible in `internal/config/config.go`, `internal/root/init.go`, `cmd/install.go`). Add `docs/plans/README.md` (one paragraph) stating these are historical/shipped plans, and prepend a `> Status: shipped (2026-03-…)` line to each existing plan file.
- **Source Role**: docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_internal/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: A reader landing here from search will reasonably believe the split is still in flight.

**9. Adopt deterministic doc-hygiene tooling (typos + dead-link)**
- **Action**: Add a `make docs-check` target that runs `typos` (or `codespell`) and `lychee` (or `markdown-link-check`) across all top-level `*.md` files. Wire into `make check` alongside the existing `check-docs`. Items #1's typos (`ataem`, `transaprency`) and broken URL (`voidexpr/ateam.git`, and the suspect `https://developers.openai.com/codex/cli` link at `README.md:100`) would have been caught mechanically.
- **Source Role**: docs_external (2026-05-14_13-13-11), docs_internal (2026-05-14_13-13-29)
- **Source Report**: .ateam/roles/docs_external/report.md, .ateam/roles/docs_internal/report.md
- **Priority**: P2
- **Effort**: SMALL
- **Rationale**: One tool adoption replaces this class of finding in future review cycles. Both reports independently recommended this kind of automation.

### Deferred

- **Lift an `ARCHITECTURE.md` out of `DEV.md`** (docs_internal Finding 7): A real architecture index would help onboarding, but it's a MEDIUM-effort restructure that overlaps with the higher-leverage README/ROLES work above. Worth doing once the README cleanup settles.
- **Shrink the README "Isolation" section by linking to `ISOLATION.md`** (docs_external Finding 9): MEDIUM-effort consolidation; touches both files and the diagram. Sensible but lower-impact than the broken URL / Go version drift, and risks merge conflict with any in-flight isolation work.
- **`ateam install` vs `install.sh` vs `ateam init` clarification** (docs_external): A one-line note in Quick Start would help, but the underlying tri-concept naming may warrant its own decision before papering over it with docs. Bundle with the next install-flow change.
- **Move `// Package secret …` doc to `internal/secret/doc.go`** (docs_internal Finding 8): Pure nit. Add to a "nits" list rather than a Priority Action.
- **Add inline `// LEGACY:` markers at compatibility-shim sites** (docs_internal Finding 9): Useful but small, and only pays off when a shim is actually deleted. Lower priority than the user-facing fixes.
- **Confirm Codex CLI URL at `README.md:100`** (docs_external): Folded into the dead-link tooling action (#9) rather than a separate task — the tool will surface it if it 404s.

### Conflicts

No direct conflicts between the two reports. Where both reports flagged the same item (Go version, `APPROACH.md`, Tips and Tricks TODO, `ROLES.md` naming), their recommendations were consistent and have been bundled into single actions above.

### Notes

- Both reports describe a mature, well-documented project; the findings are concentrated in a small number of high-visibility surfaces (README first half, `APPROACH.md`, `ROLES.md`). Fixing the five P0/P1 items above would close most of the surface area.
- `make docs` already auto-generates `ROLES.md` from `cmd/roles.go --docs` (recent commit `ccd5003` shows active maintenance). Action #5 lives in that generator, not in the markdown file directly — touching `ROLES.md` by hand would be undone on the next `make docs`.
- The repeated `_` vs `.` role-naming finding suggests an underlying decision is pending (consolidate to one family, or formally deprecate one). That product decision is out of scope for this review; the doc preamble in #5 is a stopgap that makes the current state navigable without preempting the decision.
- The typos + broken URL + dead-link findings collectively argue for tooling (#9) over per-incident fixes. Once `make docs-check` lands, future docs cycles can read tool output instead of re-running an LLM audit for the same class of issue.
