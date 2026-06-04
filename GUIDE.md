# Guide

How to actually use ATeam day-to-day. The [README](README.md) covers what it is; this covers how to fit it into your workflow.

## When to use ateam (and when not to)

**Use ateam for:**
- Quality work you'd rather not do yourself: tests, refactors, doc updates, dependency audits, security review
- Background passes you don't want to babysit (lunch, end-of-day, weekly)
- Scripts chaining coding agents (adversarial review, blackbox testing, info gathering + summarization)
- Vibe-coded projects you want to bring closer to "production-ready"

**Use interactive agents instead for:**
- Feature development
- Anything iterative: refactors with tradeoffs, design decisions
- Debugging that needs back-and-forth

ATeam is for tasks where you'd rather not spend attention. Otherwise, talk to the agent directly.

## Recipes by cadence

Pick by how often it should run and how much you want it to do.

#### Lunch-break (15–30 min, focused)
```bash
ateam all --roles code.recent,test.recent
```
Pass on what changed this morning. Cheap, low-risk.

#### End of day (adds doc drift)
```bash
ateam all --roles code.recent,test.recent,docs.external
```

#### Mid-week (slower roles)
```bash
ateam all --roles project.dependencies,project.security
```
Dependency rot and security smells — heavier, more findings.

#### Weekly thorough
```bash
ateam all --roles code.bugs,test.gaps,project.security,project.dependencies,design.architecture
```

## Recipes by control level

#### Step by step (review before coding)
```bash
ateam report
ateam review --print              # inspect findings
# optionally edit .ateam/shared/review.md to drop or add tasks
ateam code && ateam verify
ateam serve                       # browse all artifacts
```

#### Reuse reports, run another round of coding
```bash
ateam review --extra-prompt "same reports, another round of improvements"
ateam code && ateam verify
```
Cheaper than re-running the reports.

## Recipes as shell scripts

`ateam exec` and `ateam parallel` are the primitives — compose anything.

#### Adversarial review (Codex critiques, Claude implements)
```bash
ateam exec "critical review of recent changes into review.md" --agent codex-high
ateam exec "review.md → apply fixes, commit each separately"  --agent claude-high
ateam exec "look at the recent commits for regressions"        --agent codex
```

#### Custom mini-workflow with shell glue
```bash
ateam exec "find bugs into .ateam/roles/find_bugs/bugs.md" --agent codex
ateam exec "review bugs.md, fix in separate commits, explain ones you disagree with"
ateam exec "/simplify" --agent claude
make test || say "help me"
```

#### Parallel info gathering + summarization
```bash
ateam parallel "gather X into info/x.md" "gather Y into info/y.md"
ateam exec "summarize info/*.md into info.md"
```

## Steering ateam

#### Ad-hoc (this run only)

Every prompt-taking command accepts three text-or-`@file` flags:
- `--extra-prompt TEXT` — appended inside the assembled prompt
- `--pre-prompt TEXT` — wrapped at the front, outermost
- `--post-prompt TEXT` — wrapped at the end, outermost

```bash
ateam all --extra-prompt "focus on changes to the auth model"
```

#### Persistent (across runs)

Composable fragments at project or org level:
- `.ateam/prompts/report/NAME.post.extra.md` — appends to a role prompt
- `.ateam/prompts/review.post.extra.md` — appends to the supervisor's review prompt
- Org-level: same paths under `.ateamorg/`

See [CONFIG.md](CONFIG.md) for the full override system.

## Picking roles

Roles are opt-in via `.ateam/config.toml`. `ateam auto-setup` detects your stack and enables a reasonable default set.

Not every role makes sense every run:

| Tier | Roles | Typical cadence |
|------|-------|-----------------|
| Cheap, focused | `code.recent`, `test.recent` | Lunch / on demand |
| Moderate | `code.bugs`, `test.gaps`, `docs.*` | End of day |
| Slower or noisier | `project.security`, `project.dependencies`, `design.architecture`, `code.structure` | Weekly |

Any role can be run by name regardless of its default state:
```bash
ateam report --roles code.bugs,test.quality
```

Full catalog: [ROLES.md](ROLES.md).

## Git workflow

ATeam only does `git commit`, during `code` and `verify`. The surrounding workflow is up to you:

- **Simplest:** run in your working directory, review commits, push
- **Worktree:** run in a separate `git worktree`, review, merge/cherry-pick
- **Branch:** dedicated branch in a separate clone

If you don't like a commit:
```bash
ateam exec "review commit ABC123 because of X"
# or resume the original run interactively:
ateam ps                          # find the exec_id
ateam resume EXEC_ID
```

## Costs

ATeam tracks tokens and dollars per run. `ateam cost` aggregates across runs.

If you're on a subscription the dollar figure is illustrative, but still useful: compare roles, prompt variations, or agents to find what's worth running often vs. weekly. See [APPROACH.md](APPROACH.md#tokens-as-the-metric) for why this matters.

## Troubleshooting

| Command | What it does |
|---------|--------------|
| `ateam ps` | Recent runs and their status |
| `ateam tail` | Live output of a running agent |
| `ateam inspect EXEC_ID` | Full execution details + logs |
| `ateam inspect EXEC_ID --auto-debug` | Agent reads the failure and proposes a fix |
| `ateam prompt --role NAME` | Show the exact assembled prompt |
| `ateam env` | Config and environment status |

## Tips

- **Roles are just markdown files.** Edit them. Copy them. Add your own under `.ateam/prompts/report/` (or `.ateamorg/` for org-shared).
- **Reports are markdown files.** Edit them between `report` and `review` to nudge prioritization.
- **Edit the review** between `review` and `code` to drop tasks you don't want or add ones you do.
- **Use `--profile docker`** if your tooling fights with the default sandbox.
- **Use `ateam serve`** to browse history visually; **`ateam export`** for a static single-file snapshot you can publish.
- **Break long runs into pieces** — `report`, then `review`, then `code` — so you can intervene where it matters.
