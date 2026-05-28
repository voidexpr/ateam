# Feature: gh-pr.py — Python rewrite of the GitHub PR workflow

## Goal

Replace `gh-pr.sh` with a Python script (`gh-pr.py`, `uv run --script`) that manages
the full GitHub PR lifecycle via `ateam exec`. Uses `ateam.py` from
`../metaproject/ateam.py` as the runner framework and `click` for the CLI.

Two implementation approaches are documented at the end of this spec. The file
layout and command design apply to both.

---

## Commands

| Command | Agent? | Description |
|---------|--------|-------------|
| `fetch [PRID]` | no | Download latest PR state; create new TIMESTAMP dir if anything changed |
| `review [PRID]` | yes | Triage new comments → `comments_review.md` |
| `apply [PRID]` | yes | Implement the review plan → `apply_report.md` |
| `compose [PRID]` | yes | Synthesize all unpushed work → `compose.md` |
| `push [PRID]` | no | git push + post GitHub comments |
| `status [PRID]` | no | Show pipeline state; without PRID lists all known PRs |
| `create` | yes | Draft and optionally open a new PR from current branch |
| `respond [PRID]` | both | Compound: fetch → review → apply → compose |
| `set-pr PRID` | no | Update `current_pr.json` to set the default PR |

**Global flags:**

| Flag | Applies to | Meaning |
|------|-----------|---------|
| `--resync` | review, respond | Full-scan mode: read entire PR history + git log, not just delta |
| `--force` | any | Bypass staleness skips |
| `--focus PROMPT` | review, respond | Inject extra instruction; forces re-review |
| `--push` | compose, respond | Run `push` immediately after compose |
| `--dry-run` | any | Print assembled prompts without calling `ateam exec` |

`PRID` always defaults to the value in `current_pr.json` when omitted.

---

## File layout

```
.ateam/shared/gh-pr/
  current_pr.json              ← {prid: 174, repo: "jy-tan/fence"}
                                 updated by 'create' and 'set-pr'

  PRID/
    TIMESTAMP/                 ← one dir per distinct PR state (see TIMESTAMP below)
                                 contains per-batch processing artifacts only

      fetched/                 ← raw GitHub API data, never modified after fetch
        pr.json
        diff.patch
        reviews.json
        inline-comments.json
        pr-comments.json
        files.json

      new_comments.json        ← delta: items not present in any previous TIMESTAMP
                                 [] if nothing new since last TIMESTAMP

      fetch_meta.json          ← {prev_timestamp, content_hash}

      comments_review.md       ← agent output: triage of THIS batch's new items
      review_meta.json         ← {exec_id, timestamp}

      apply_report.md          ← agent output: work done for THIS batch
      apply_meta.json          ← {exec_id, timestamp, start_commit, end_commit}

      per_finding_replies.json ← push-back drafts for THIS batch's items

    compose.md                 ← PRID-level: spans all unpushed TIMESTAMPs
    compose_meta.json          ← {exec_id, timestamp, head_sha,
                                   covered_timestamps: [...]}

    push_log.md                ← PRID-level: record of what was published
    push_meta.json             ← {timestamp, pushed_sha,
                                   posted: [...], manual: [...]}

  create-BRANCH/               ← for 'create' only (no PRID yet)
    commits.txt
    diff.patch
    spec.md                    ← optional --spec FILE input
    pr_draft.md                ← agent output: title + body
    submit.sh                  ← gh pr create command (review before running)
```

---

## TIMESTAMP

The TIMESTAMP directory name is derived from `pr.json`'s `updatedAt` field
(UTC, formatted `YYYY-MM-DD_HHMMSS`). This field advances on any PR activity:
commit, comment, review, resolution.

`fetch` behavior:
1. Download all PR data to a temp directory.
2. Read `updatedAt` → candidate TIMESTAMP.
3. If `PRID/TIMESTAMP/` already exists: nothing changed on GitHub, exit cleanly.
4. If not: create `PRID/TIMESTAMP/fetched/`, copy data, compute `new_comments.json`,
   write `fetch_meta.json`.

---

## new_comments.json

Computed by `fetch` by diffing the new fetch against the most recent previous
TIMESTAMP directory (comparing comment IDs and review IDs). On first fetch:
all items. On subsequent fetches: only items not seen in any previous TIMESTAMP.

---

## Staleness rules

| Step | Skips when |
|------|------------|
| `fetch` | TIMESTAMP dir already exists (content unchanged) |
| `review` | `new_comments.json` is `[]` AND `comments_review.md` exists AND no `--force`/`--focus`/`--resync` |
| `apply` | `apply_report.md` newer than `comments_review.md` |
| `compose` | `compose.md` exists AND `head_sha` in `compose_meta.json` == `git rev-parse HEAD` AND latest TIMESTAMP dir is in `covered_timestamps` |
| `push` | requires `compose.md`; idempotent via `posted` flags in `push_meta.json` |

`apply_meta.json` is written by the Python script layer (not the agent):
`start_commit` recorded before launching the agent, `end_commit` recorded after.
Staleness checks read `*_meta.json` files only — never parse agent-written markdown.

---

## Step details

### fetch
Pure Python. Described above under TIMESTAMP. No agent.

### review

**Normal mode**: agent reads `new_comments.json` (delta) and `fetched/*` for thread
context. Also reads previous TIMESTAMP's `comments_review.md` to understand what
was already triaged. Writes `comments_review.md` covering only new items.

**`--resync` mode**: agent reads the full `fetched/*` (all comments, all reviews,
all threads) plus `git log <base_branch>..HEAD` and all previous TIMESTAMP dirs'
`apply_report.md`. Determines what is still outstanding vs. already addressed.
Produces `comments_review.md` scoped to what genuinely still needs attention.
Used when jumping into a PR mid-stream, after manual work, or after another tool
was used for a previous round.

If `--resync` produces an empty `comments_review.md` (everything already addressed),
`apply` skips immediately and `compose` runs to produce the response.

### apply
Skips if `apply_report.md` newer than `comments_review.md`.

Script records `start_commit` before launching agent, `end_commit` after.

Agent reads `comments_review.md` and `fetched/inline-comments.json` (for comment
IDs and thread URLs). Makes code changes, commits per-finding. Writes:
- `apply_report.md`: applied / enhanced / pushed-back items + build/test results
- `per_finding_replies.json`: structured push-back reply drafts

Script writes `apply_meta.json`: `{exec_id, timestamp, start_commit, end_commit}`.

### compose

Skips if `compose.md` exists AND `HEAD == compose_meta.json.head_sha` AND latest
TIMESTAMP is already in `compose_meta.json.covered_timestamps`.

Runs when: `compose.md` missing, HEAD moved since last compose, or a new TIMESTAMP
exists that hasn't been covered yet.

Agent reads:
- ALL unpushed TIMESTAMP dirs' `apply_report.md` and `per_finding_replies.json`
  (unpushed = any TIMESTAMP with mtime newer than `push_meta.json`, or all if no push yet)
- `git log <pushed_sha>..HEAD` — captures apply commits + any extra manual work
- `fetched/pr.json` from latest TIMESTAMP for PR URL and metadata

Agent writes:
- `compose.md`: complete reviewer-facing response covering all unpushed work
- `final_replies.json`: ready-to-post comment list (see format below)

Script writes `compose_meta.json`: `{exec_id, timestamp, head_sha, covered_timestamps}`.

This design means new comments arriving before a push don't require re-applying
old work: the new TIMESTAMP gets its own review + apply, and compose spans both.

### push
Pure Python. Requires `compose.md` and `final_replies.json`.

1. `git push origin <head_branch>`.
2. For each unposted entry in `final_replies.json`: attempt `gh` command; on
   permission failure print URL + body for manual posting; on success mark posted.
3. Write `push_log.md` and `push_meta.json`.

Idempotent: re-running skips already-posted entries.

Token note: fork-only PAT cannot post inline replies or PR-level comments to
upstream (403 on both). Both are attempted; both fall back to manual-posting output.

### compose and push with `--push`
Running `compose --push` or `respond --push` runs compose then push in one shot.
Useful for the common case where you've reviewed `apply_report.md` and are ready
to publish.

### status
Reads current TIMESTAMP dir and PRID-level files. Prints pipeline state and next
recommended command.

```
PR #174  jy-tan/fence  feat/handle-ctrl-z → main
  fetch    ✓  2026-05-26_091500  (3 new comments)
  review   ✓  2026-05-26_091800  (3 items: 2 APPLY, 1 PUSH BACK)
  apply    ✓  2026-05-26_101500  (start: abc123  end: def456)
  compose  ✗  HEAD moved (2 extra commits since apply)
  push     -  no compose.md
  next:    compose
```

Flags: `review STALE` / `apply BEHIND` / `compose PENDING` / `push PENDING` /
`push PARTIAL`.

Without PRID: one-line status per known PR dir.

### respond
Runs fetch → review → apply → compose in sequence, each using normal skip logic.
`--resync` propagates to review. `--push` runs push at the end.

### create
Pre-fetch (no agent): verify no open PR exists, determine base branch, write
`commits.txt` and `diff.patch`. Agent writes `pr_draft.md` and `submit.sh`.
With `--push`: runs `submit.sh` then calls `set-pr`.

---

## final_replies.json format

```json
[
  {
    "finding": "Guard jobControlEnabled on TIOCSPGRP success",
    "type": "inline-reply",
    "comment_id": 1234567,
    "thread_url": "https://github.com/jy-tan/fence/pull/174#discussion_r1234567",
    "body": "...",
    "posted": false
  },
  {
    "finding": "overall",
    "type": "pr-comment",
    "pr_url": "https://github.com/jy-tan/fence/pull/174",
    "body": "Summary of all work done in this round...",
    "posted": false
  }
]
```

---

## GitHub API and token setup (carry from gh-pr.sh)

- Require `GH_TOKEN`; print full setup instructions from `gh-pr.sh` lines 107–155
  on missing token (fine-grained PAT scopes, fork-only limits explained).
- Set `GH_CONFIG_DIR=.ateam/cache/gh-config` to isolate from `~/.config/gh`.
- Validate with `gh auth status` on startup.
- Repo resolution: try current repo, fall back to `parent.nameWithOwner` for forks.
- Defensive empty-result checks after every `gh api` call (carry from `prefetch_pr`).

---

## What is dropped from gh-pr.sh

- `--restart` and `backup()` — replaced by TIMESTAMP directory scheme.
- Shared `cache/` directory — `fetch` uses a Python tempdir internally.
- `submit.sh` from `do-feedback` — replaced by `final_replies.json` + `push`.
- `review-feedback` / `do-feedback` names — replaced by `review` / `apply`.
- Single `state.json` shared across steps — replaced by per-step `*_meta.json`.

---

## Implementation approaches

### Approach A: Orchestrated pipeline (gh-pr.py)

The design described above. A Python CLI script with explicit commands. Each step
is an independent `ateam exec` call. The Python layer handles all skip logic,
staleness detection, metadata recording, and artifact routing. Agents receive
focused inputs and produce focused outputs.

**Strengths:**
- **Unattended operation**: run `respond` and return to a completed result
- **Auditability**: `exec_id` in every `*_meta.json`, immutable TIMESTAMP dirs
- **Resumability**: any step can be re-run independently from any checkpoint
- **Cost control**: agents only run when their specific input has changed
- **Deterministic flow**: skip logic is Python, not LLM reasoning
- **Human checkpoints**: by default you review artifacts between steps before proceeding

**Weaknesses:**
- **Rigidity**: edge cases require new flags (`--resync`, `--force`) or command combinations
- **Context fragmentation**: each agent call starts cold, reads context from files
- **Orchestration complexity**: the Python code encodes significant workflow logic
- **Latency**: multiple sequential `ateam exec` round-trips for `respond`

**Best for**: automated or semi-automated workflows where you want predictable,
auditable, resumable execution and don't mind a richer CLI surface.

---

### Approach B: Claude Code with PR skills

Use Claude Code itself (not a custom agent) as the interactive runtime. Give it
a small set of Claude Code skills (slash commands) that wrap `gh` CLI calls for
the mechanical parts. Claude reasons freely about what to do, calls skills when
it needs to fetch data or post comments, makes code changes directly, and asks for
confirmation before irreversible actions.

**Prior art**: `/babysit-pr` (gabrielshanahan, tilomitra) is the closest existing
skill — it monitors a PR, addresses review comments, fixes CI failures, posts
replies via `gh`, all inside a single Claude Code session. Uses companion bash
helpers for fetch/reply/mark-processed. `corylanou/address-pr-comments` does
the same for a single review pass using GraphQL to fetch unresolved threads.
Both confirm the pattern works in practice.

**Skills to write (each is a short SKILL.md + optional bash helper):**
- `/gh-pr-fetch PRID` — runs `gh` API calls, writes to the standard TIMESTAMP
  artifact layout; prints a summary of new comments found
- `/gh-pr-post PRID` — reads `final_replies.json`, posts via `gh`, falls back to
  printing for manual posting on 403; marks posted entries
- `/gh-pr-status PRID` — reads artifact files, prints the same status table as
  Approach A's `status` command

Claude uses standard file tools to read/write `comments_review.md`,
`apply_report.md`, `compose.md`. It calls `gh` directly for anything not covered
by the skills. The artifact schema from this spec is the shared protocol — both
approaches produce the same files, so work started in one can be continued in the other.

A typical session looks like:
```
> /gh-pr-fetch 174
  → 3 new comments found, written to .ateam/shared/gh-pr/174/2026-05-26_091500/

Claude reads the fetched data, triages the comments, makes code changes,
writes comments_review.md + apply_report.md, asks "ready to push?",
then calls /gh-pr-post when confirmed.
```

**Strengths:**
- **No orchestration code**: no Python pipeline to maintain; Claude drives the flow
- **Flexibility**: handles any scenario naturally; "figure out where we are" is
  just part of the prompt, no `--resync` flag needed
- **Single context**: reasons across all information at once, no fragmentation
- **Skill reuse**: `/gh-pr-fetch` and `/gh-pr-post` can be used standalone or
  as building blocks in other workflows
- **Same artifacts**: produces the same TIMESTAMP dir layout as Approach A,
  so the two approaches are interchangeable mid-PR

**Weaknesses:**
- **Requires supervision**: needs a human present; unattended `respond`-style
  operation is not natural here
- **Less auditable**: no `exec_id` in meta files (Claude Code sessions are logged
  but not tied to artifact files without extra instrumentation)
- **Context window limits**: very large PRs (many comments + large diff) may
  exhaust the window mid-session
- **Non-deterministic ordering**: Claude may approach the same situation
  differently across sessions
  in structured `*_meta.json` files
- **Non-deterministic**: the agent may approach the same situation differently each time,
  making debugging harder
- **Context window limits**: a PR with many comments and a large diff may fill the window
- **Cost**: one long interactive session may be more expensive than targeted agents,
  and cost is harder to predict
- **Harder to resume**: if interrupted, re-spawning the agent means re-reading all
  state from scratch

**Best for**: exploratory or one-off PR work where flexibility matters more than
predictability, or as a fallback when the pipeline hits an edge case it can't handle.

---

### Approach C: Comment-centric task system + pr-work skill

A fundamentally different data model. Instead of tracking PR state as batch
artifact files, each GitHub comment becomes an individual task with its own
lifecycle. The task system is the source of truth; `gh-pr.py fetch` syncs
GitHub into it; Claude Code's `/pr-work` skill drives the workflow.

**Dependency: `mdtrack`** — a task-tracking CLI that stores tasks as structured
markdown. Must exist before this approach can be implemented. Required interface:

```bash
mdtrack edit TASK_ID --tag-add VALUE --tag-remove VALUE --status NEW_STATUS
mdtrack comment add TASK_ID [--tags VALUE] <<EOF
comment body
EOF
mdtrack list --area pr/PRID --status pending    # returns markdown task list
```

#### `mdtrack` interface used

`mdtrack` is a markdown-backed task database with a SQLite store. Tasks are
created and updated by writing markdown files with binding blocks and running
`mdtrack save FILE`. There is no `edit` command — state changes go through:

```bash
mdtrack view --id TASK_ID --out /tmp/task.md   # get current markdown
# agent edits the binding block (tags, status, etc.)
mdtrack save /tmp/task.md                       # upsert back
```

To add a reply comment to a task:
```bash
mdtrack comment add TASK_ID --tag reply <<EOF
The reply text to post on GitHub.
EOF
```

To read pending work:
```bash
mdtrack view --project jy-tan/fence --tag pending   # markdown of pending tasks
```

#### Task schema

Each GitHub comment or review item maps to one `mdtrack` task:

| Field | Content |
|-------|---------|
| `project` | repo name (e.g. `jy-tan/fence`) |
| `area` | `pr/PRID/TYPE` where TYPE is `inline`, `review`, `pr-comment` |
| `title` | **exact GitHub comment URL** (e.g. `https://github.com/…/pull/174#discussion_r1234567`) — used by `push` to know where to post |
| `body` | verbatim upstream comment text |
| `status` | `todo` (default); set to `done` after pushed |
| `tags` | see lifecycle below |
| `author` | GitHub login of the commenter — `fetch` skips tasks where author == own login to prevent reply loops |

`priority`, `effort`, `owner` are not used for PR comments.

#### Tag lifecycle

```
pending   →   reviewed   →   applied   →   pushed
```

- **pending**: downloaded from GitHub, not yet processed by agent
- **reviewed**: agent has added a `reply`-tagged comment to the task
  (for push-back: the reply text itself; for apply items: the reply text
  with `[commit: TBD]` placeholder)
- **applied**: `[commit: TBD]` in the reply comment has been replaced with
  an actual commit SHA; code change is in the local branch
- **pushed**: `gh-pr.py push` has posted the reply to GitHub

Additional tags:
- **wont-fix**: explicit decision not to address; no reply needed
- **extra-work**: local commit not tied to any upstream comment (e.g. from
  an independent code review); included in the overall push summary

#### The reply comment is the unit of truth

The `reply`-tagged comment on each task is exactly what `gh-pr.py push` will
post to the GitHub URL in the task title. The agent's core job is: **ensure
every non-wont-fix task has a reply-tagged comment**.

For apply/enhance items: the reply initially contains `[commit: TBD]`. After
the code change is committed, the agent adds a new reply comment (or replaces
the placeholder) with the actual SHA. The dedup on `comment add` is exact-match,
so changing the body creates a new comment — the old one is superseded.

#### `gh-pr.py` commands for Approach C

| Command | Description |
|---------|-------------|
| `gh-pr.py fetch [PRID]` | Download PR data; upsert new comments as `pending` tasks. Matches existing tasks by GitHub comment ID (stored in `area`). Skips comments where `author` == own login. Writes raw data to TIMESTAMP dir for lineage. Updates `current_pr.json`. |
| `gh-pr.py pending [PRID]` | `mdtrack view --project REPO --area pr/PRID --no-tag pushed,wont-fix` — shows tasks still needing work |
| `gh-pr.py compose [PRID]` | Agent step: reads all `applied`, `reviewed`, and `extra-work` tasks (their `reply` comments) + `git log <last_push_sha>..HEAD`; writes `compose.md` at the PRID level with the overall reviewer-facing summary. Skips if `compose.md` is newer than the last `applied`/`reviewed` task update and HEAD hasn't moved. |
| `gh-pr.py push [PRID]` | Requires `compose.md`. git push + for each task tagged `applied` or `reviewed` (push-back): read the `reply` comment, post to the GitHub URL in the task title, transition tag to `pushed`, set status `done`. Finally posts `compose.md` content as a PR-level comment. Falls back to printing URL + body on 403. |
| `gh-pr.py status [PRID]` | Count tasks by tag. Show HEAD sha and whether branch is ahead of remote. Flag if `compose.md` is missing or stale. |

`fetch` still writes raw GitHub data to a TIMESTAMP dir (`fetched/` only) for
lineage. The task DB is the live state.

#### The `/pr-work PRID` skill

A Claude Code SKILL.md that drives the full session:

```
1. gh-pr.py fetch $PRID
   → upserts new GitHub comments as pending tasks

2. gh-pr.py pending $PRID
   → get markdown of all tasks still needing work

3. For each pending task (agent decides):

   PUSH BACK:
   - Write a reply comment explaining the decision
   - mdtrack comment add TASK_ID --tag reply <<< "reply text"
   - Update task: remove tag pending, add tag reviewed
   - mdtrack view --id TASK_ID --out /tmp/t.md && edit tags && mdtrack save /tmp/t.md

   APPLY / ENHANCE:
   - Write a placeholder reply comment
   - mdtrack comment add TASK_ID --tag reply <<< "Addressed in [commit: TBD]"
   - Update task: pending → reviewed
   - Make the code change, commit
   - Add a new reply comment with the actual SHA (supersedes placeholder)
   - mdtrack comment add TASK_ID --tag reply <<< "Addressed in commit abc123: ..."
   - Update task: reviewed → applied

   WONT-FIX:
   - Update task: add tag wont-fix, set status done
   - No reply comment needed

4. For extra local work (independent commits, extra code review):
   - Make the change, commit
   - Add an extra-work task or tag an existing related task
   - mdtrack comment add TASK_ID --tag extra-work <<< "commit abc123: ..."

5. gh-pr.py compose $PRID
   → agent reads all applied/reviewed/extra-work tasks + git log,
     writes compose.md (the overall reviewer-facing summary)
   → review compose.md before proceeding

6. "Ready to push? Run: gh-pr.py push $PRID"
   gh-pr.py push posts all per-task reply comments then compose.md
   as the final PR-level comment.
```

**Cold start / resync**: no special flag. `gh-pr.py fetch` upserts all open
comments as `pending` if not already tracked. Already-addressed comments are
`applied` or `pushed` — they don't appear in `pending` output. `/pr-work` picks
up exactly where any previous work (manual or scripted) left off.

**New comments after partial work**: `fetch` adds them as new `pending` tasks.
Old tasks keep their existing tags. `pending` shows only the new ones.

**Extra commits**: agent adds `extra-work` tasks. `gh-pr.py push` includes them
in the overall summary comment.

#### Strengths

- **Comment-level granularity**: each item has its own state; nothing gets
  lost in batch files
- **Cold start / resync is free**: pending = not yet done, no reconstruction needed
- **New comments are just more pending tasks**: no TIMESTAMP staleness logic
- **Extra work integrates naturally**: just another task with `extra-work` tag
- **Author loop prevention built in**: `fetch` skips tasks where `author` == own
  GitHub login
- **Resume anywhere**: Claude reads `gh-pr.py pending`, picks up exactly where it left off
- **Audit trail**: task comments record every decision + commit SHA; TIMESTAMP
  dirs hold the raw fetched data

#### Weaknesses

- **Requires `mdtrack`**: additional dependency that must exist and be functional
- **Less temporal batch view**: harder to reconstruct "what did round 2 look like
  as a whole" — you see individual task histories, not a batch narrative
- **No exec_id tracking** unless task comments include it explicitly
- **Push response authoring**: `gh-pr.py push` needs to synthesize the overall
  reply from task comments — this is the `compose` step compressed into a
  mechanical script, which may produce a less coherent response than an agent
  writing `compose.md` with full context

`gh-pr.py compose` closes the gap: an agent reads all processed tasks and their
`reply` comments plus the full git log and writes `compose.md` — the same
artifact as Approach A, with the same quality of synthesis. The input is cleaner
than in A (structured task comments rather than batch `apply_report.md`) and the
skip logic is simpler (stale if any task updated since last compose, or HEAD moved).

---

### Comparison

| Concern | Pipeline (A) | Skill + Claude Code (B) | Task system (C) |
|---------|-------------|------------------------|-----------------|
| Data model | batch artifact files | batch artifact files | per-comment tasks in DB |
| Unattended operation | ✓ | ✗ needs supervision | partial (fetch/push mechanical) |
| Cold start / resync | `--resync` flag | natural | natural — pending = not yet done |
| New comments mid-flight | TIMESTAMP + compose span | natural | natural — new `pending` tasks |
| Extra local work | git log in compose | natural | `extra-work` tasks |
| Auditability / lineage | exec_id + TIMESTAMP dirs | session logs | task + comment history in DB |
| Resumability | any checkpoint | restart from scratch | `pending` output, continue |
| Author loop prevention | manual in prompt | manual in prompt | built-in (`author` field in fetch) |
| State update mechanism | file mtime | none | `view` → edit binding block → `save` |
| Reply authoring | agent writes compose.md | agent composes freely | agent adds `reply` comment per task |
| Overall push summary | agent writes compose.md | agent composes freely | agent writes compose.md from task comments |
| Dependencies | Python + ateam | Claude Code + gh | Python + ateam + mdtrack |
| Batch narrative history | ✓ TIMESTAMP dirs | ✗ | ✗ (per-task history only) |
