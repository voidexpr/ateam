# Prompt Example: `crawl.sh` Across Three Approaches

Source example: `../agent-release-monitoring/crawl.sh` (127 lines)

This is a release-notes crawler. For each of N tools (Claude Code, Codex CLI),
it queries an `mdtrack` SQLite DB for known versions, builds a prompt asking
the agent to find newer versions and write per-version reports, runs the agent,
and on failure pings macOS notifications + `ateam inspect --auto-debug`.

It's a tighter workload than `metaproject.py`: one workflow, two tool variants,
prompt mostly static. Useful contrast.

## Approach 1: Shell script (the original, ~127 lines)

The current version is one file. Two helper functions (`ccdb`, `known-versions`)
and one main function (`crawl-tool`) that:
1. Echoes "before" state.
2. Builds the prompt as a heredoc, interpolating tool name / sources / DB
   contents.
3. Pipes to `ateam exec --profile codex` with `||` for failure handling.
4. Echoes "after" state.

Called twice at the bottom — once per tool.

```bash
# (Original; see ../agent-release-monitoring/crawl.sh)
```

**Strengths:**
- One file. Copy → modify → run. No imports.
- Heredoc string interpolation works fine for a static prompt with a few
  inserted values.
- Failure handling is a chained `||` block — short and visible.

**Weaknesses:**
- The prompt body and the dispatcher logic share one indentation context. Any
  prompt edit means scrolling past bash function plumbing.
- Adding a second concern around the prompt (e.g. a pre-flight check) means
  more lines of bash interleaved with the heredoc.
- No structured skip / error result — failure is "the subprocess exited
  non-zero," which conflates "no work to do" with "something broke."

## Approach 2: File-based using the prompt-filesystem proposal

Lay out prompt content as files; orchestration in a tiny dispatcher.

### Filesystem layout

```
.ateam/prompts/release_crawl/
  _pre.intro.md                    # goal + last-3-versions ({{shell ...}})
                                   # frontmatter declares dir-level pre_exec
  _post.format.md                  # the long report-format block (shared)
  _post.outcome.md                 # SUCCESS / FAILED instruction (shared)
  claude_code.prompt.md            # Claude Code-specific (sources + tool name)
  codex.prompt.md                  # Codex CLI-specific
  scripts/
    list-known.sh                  # used by {{shell}} to inline last 3 versions
    check-new.sh                   # pre_exec: skip if no new version available
    notify-fail.sh                 # used by dispatcher on failure
```

### `_pre.intro.md`

```markdown
---
pre_exec:
  - ./scripts/check-new.sh {{prompt.name}}
---
# Goal

Your goal is to help me track new features in areas I care about in this
tool's release notes.

# Last 3 Processed Versions

{{shell ./scripts/list-known.sh {{prompt.name}}}}
```

The frontmatter at the top of any `_pre.*.md` or `_post.*.md` file declares
dir-level `pre_exec` / `post_exec`. If multiple dir-level structural files
carry frontmatter, the lists merge.

### `claude_code.prompt.md`

```markdown
Tool: Claude Code
Project: ccversions
DB file: ccversions.sqlite

Known sources:
* [code.claude.com/docs/en/changelog](https://code.claude.com/docs/en/changelog)
* if needed: GitHub mirror (github.com/anthropics/claude-code)

If sources change without notice, find new ones. If you have no network, terminate.
```

### `_post.format.md`

```markdown
# Steps

## 1. Check for new versions

If there are no known versions only process the 3 most recent.
If all versions have been processed you are done.

## 2. Process new versions

For each version more recent than the most recent version listed above
(i.e. not processed yet) produce a per release report file.

General Guidelines:
* All caps terms below are substituted with information from your findings
* Be short, details can be obtained from the release notes

Report Guidelines:
* For each version write your report in a file named EXACT_VERSION.md
* Report format is markdown with a strict h2 title structure:

    ## PROJECT - EXACT_VERSION - done

    * Released on: YYYY-MM-DD
    * Link: EXACT_DIRECT_LINK_HERE
    * Reviewed on: YYYY-MM-DD HH:MM

    ### Leak: fd, child processes, memory, cpu spins
    PUT DETAILS THAT CHANGED HERE

    ### oauth
    PUT DETAILS THAT CHANGED HERE

    [... other H3 sections — same as original ...]

    ### Other
    ONLY MAJOR CHANGES NOT ALREADY MENTIONED

### Run a bash command for each new version report file:

    mdtrack --db DB_FILE add ./EXACT_VERSION.md
```

### `_post.outcome.md`

```markdown
## 3. Print outcome

In your last message specify:

    SUCCESS: found N new versions
    FAILED: describe what happened
```

### `scripts/check-new.sh`

```bash
#!/usr/bin/env bash
# Skip the LLM run if no new release is available.
set -euo pipefail

prompt_name="$1"
# Map prompt name → project + first source URL (kept tiny; could be a TOML)
case "$prompt_name" in
  claude_code) project=ccversions; url=https://code.claude.com/docs/en/changelog ;;
  codex)       project=codex_versions; url=https://github.com/openai/codex/releases ;;
  *) echo "unknown prompt $prompt_name" >&2; exit 2 ;;
esac

latest_remote="$(curl -fsSL "$url" | grep -Eo 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
latest_known="$(mdtrack --db "${project}.sqlite" view --project "$project" \
                --sort -updated --limit 1 --format short | head -1)"

if [ -n "$latest_remote" ] && [ "$latest_remote" = "$latest_known" ]; then
  ateam flow skip --reason "no new $prompt_name versions"
fi
```

### `scripts/list-known.sh`

```bash
#!/usr/bin/env bash
# Inline into prompt via {{shell ./scripts/list-known.sh PROMPT}}
prompt_name="$1"
case "$prompt_name" in
  claude_code) project=ccversions; db=ccversions.sqlite ;;
  codex)       project=codex_versions; db=codex_versions.sqlite ;;
esac
mdtrack --db "$db" view --project "$project" --sort -updated --limit 3
```

### Dispatcher (the only "script" outside the prompt tree)

```bash
#!/usr/bin/env bash
set -euo pipefail
[ -d .ateam ] || ateam init

for tool in claude_code codex; do
  ateam exec :release_crawl/$tool --profile codex \
    || ./.ateam/prompts/release_crawl/scripts/notify-fail.sh "$tool"
done
```

(With `ateam parallel :release_crawl/claude_code :release_crawl/codex` you get
parallelism for free.)

### Line count (approximate)

| File | Lines |
|---:|---|
| `_pre.intro.md` (incl. frontmatter) | ~12 |
| `_post.format.md` | ~55 |
| `_post.outcome.md` | 6 |
| `claude_code.prompt.md` | 9 |
| `codex.prompt.md` | 6 |
| `scripts/check-new.sh` | ~18 |
| `scripts/list-known.sh` | ~8 |
| `scripts/notify-fail.sh` | ~6 |
| Dispatcher | ~8 |
| **Total** | **~128** |

Roughly the same line count as the shell version, **but the ~75 lines of
prompt text now live in plain Markdown files** instead of being interpolated
inside bash heredocs.

## Approach 3: Python API (on top of `ateam_runner.py`)

Single file. Uses the `Ctx` / `PromptBundle` / `Runner` framework defined in
`prompt_example_metaproject_python_api.md` (the framework is ~130 lines and
shared across workflows; not counted here).

```python
#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""crawl.py — track new tool releases via release notes."""
from __future__ import annotations

import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from ateam_runner import (
    Ctx, Flow, PromptBundle, Runner, PromptFn, SkipIf,
)

SCRIPT_DIR = Path(__file__).resolve().parent


@dataclass(frozen=True)
class Tool:
    name: str
    display: str
    project: str
    db_file: Path
    sources: str
    latest_url: str


TOOLS = {
    "claude_code": Tool(
        name="claude_code", display="Claude Code", project="ccversions",
        db_file=SCRIPT_DIR / "ccversions.sqlite",
        sources="""* [code.claude.com/docs/en/changelog](https://code.claude.com/docs/en/changelog)
* if needed: GitHub mirror (github.com/anthropics/claude-code)""",
        latest_url="https://code.claude.com/docs/en/changelog",
    ),
    "codex": Tool(
        name="codex", display="Codex CLI", project="codex_versions",
        db_file=SCRIPT_DIR / "codex_versions.sqlite",
        sources="* [github.com/openai/codex/releases](https://github.com/openai/codex/releases)",
        latest_url="https://github.com/openai/codex/releases",
    ),
}


def mdtrack(db: Path, *args: str) -> str:
    return subprocess.run(["mdtrack", "--db", str(db), *args],
                          capture_output=True, text=True, check=True).stdout

def last_3(t: Tool) -> str:
    return mdtrack(t.db_file, "view", "--project", t.project,
                   "--sort", "-updated", "--limit", "3")

def latest_known(t: Tool) -> str:
    return mdtrack(t.db_file, "view", "--project", t.project,
                   "--sort", "-updated", "--limit", "1", "--format", "short").strip().split("\n")[0] if t.db_file.exists() else ""


REPORT_FORMAT = """\
# Steps

## 1. Check for new versions

If there are no known versions only process the 3 most recent.
If all versions have been processed you are done.

## 2. Process new versions

For each version more recent than the most recent listed above produce a
per-release report file.

General Guidelines:
* All caps terms below are substituted with information from your findings
* Be short, details can be obtained from the release notes

Report Guidelines:
* For each version write your report in a file named EXACT_VERSION.md
* Report format is markdown:

    ## {project} - EXACT_VERSION - done

    * Released on: YYYY-MM-DD
    * Link: EXACT_DIRECT_LINK_HERE
    * Reviewed on: YYYY-MM-DD HH:MM

    ### Leak: fd, child processes, memory, cpu spins
    PUT DETAILS THAT CHANGED HERE

    ### oauth
    PUT DETAILS THAT CHANGED HERE

    [... other H3 sections — same as original ...]

    ### Other
    ONLY MAJOR CHANGES NOT ALREADY MENTIONED

### Run a bash command for each new version report file:

    mdtrack --db {db_file} add ./EXACT_VERSION.md

## 3. Print outcome

In your last message specify:

    SUCCESS: found N new versions
    FAILED: describe what happened
"""


def crawl_prompt(ctx: Ctx) -> str:
    t: Tool = ctx.data["tool"]
    return f"""\
# Goal

Your goal is to help me track new features in areas I care about in {t.display}
release notes.

# Last 3 Processed Versions

{last_3(t)}

Known sources:
{t.sources}

If sources change without notice, find new ones. If no network, terminate.

{REPORT_FORMAT.format(project=t.project, db_file=t.db_file)}
"""


# === Pre-exec: skip when no new version (NEW feature) ===

def has_new_version(ctx: Ctx) -> bool:
    """HTTP GET the changelog URL, parse latest version, compare with DB."""
    t: Tool = ctx.data["tool"]
    try:
        import urllib.request, re
        body = urllib.request.urlopen(t.latest_url, timeout=10).read().decode()
        m = re.search(r"v?(\d+\.\d+\.\d+)", body)
        remote = m.group(1) if m else None
    except Exception:
        return True  # On error, run anyway — let the agent figure it out.
    return remote is None or remote != latest_known(t).lstrip("v")


def on_failure(t: Tool) -> None:
    subprocess.run(["notify-macos", f"{t.display} Release notes crawl failed"], check=False)
    subprocess.run(["ateam", "inspect", "--last", "--auto-debug",
                    "--auto-debug-extra-prompt",
                    f"Explain why the agent wasn't able to produce a report about new {t.display} versions"], check=False)
    subprocess.run(["say", f"{t.display} Release notes crawl failed"], check=False)


def build_runner() -> Runner:
    r = Runner()
    for name in TOOLS:
        r.add(PromptBundle(
            name=name,
            prompt=[PromptFn(crawl_prompt)],
            pre_exec=[SkipIf(
                lambda ctx: not has_new_version(ctx),
                lambda ctx: f"{ctx.data['tool'].display}: no new versions",
            )],
        ))
    return r


def ctx_for(name: str) -> Ctx:
    return Ctx(work_dir=SCRIPT_DIR, role=name, action="crawl",
               data={"tool": TOOLS[name]})


def main() -> None:
    if not (SCRIPT_DIR / ".ateam").is_dir():
        subprocess.run(["ateam", "init"], cwd=SCRIPT_DIR, check=True)
    runner = build_runner()
    names = sys.argv[1:] or list(TOOLS)
    for name in names:
        t = TOOLS[name]
        print(f"--< Before: {t.display} >" + "-" * 40)
        print(mdtrack(t.db_file, "view", "--sort", "-updated",
                      "--format", "outline", "--project", t.project))
        try:
            result = runner.run(name, ctx_for(name))
            if result.flow.state == "skip":
                print(f"= {name}: {result.flow.reason}")
        except subprocess.CalledProcessError:
            on_failure(t)
        print(f"--< After: {t.display} >" + "-" * 40)
        print(mdtrack(t.db_file, "view", "--sort", "-updated",
                      "--format", "outline", "--project", t.project))


if __name__ == "__main__":
    main()
```

### Line count

| Part | Lines |
|---|---:|
| Tool dataclass + TOOLS dict | ~30 |
| mdtrack helpers | ~10 |
| `REPORT_FORMAT` constant (prompt text) | ~55 |
| `crawl_prompt` function | ~16 |
| `has_new_version` (NEW: pre-flight check) | ~10 |
| `on_failure` handler | ~6 |
| `build_runner` + `ctx_for` | ~14 |
| `main` (CLI + before/after echo) | ~20 |
| Imports + header | ~10 |
| **Total (script only)** | **~170** |
| Framework (`ateam_runner.py`, shared) | 130 |
| **Total including framework** | **~300** |

Script-only is ~170. The framework is amortized across all workflows that use
it; for a single workflow it inflates the "all-in" count.

## Side-by-side comparison

| Dimension | Shell (1) | Filesystem (2) | Python API (3) |
|---|---|---|---|
| **Total lines** | ~127 | ~126 across 10 files | ~170 (+ 130 framework) |
| **Files** | 1 | 10 | 1 (+ 1 shared framework) |
| **Prompt text location** | Inline heredoc | Plain Markdown files | Python triple-quoted strings |
| **Where to look first** | Top of file | `ls .ateam/prompts/release_crawl/` | `crawl_prompt` function |
| **Adding a new tool** | Copy a line at the bottom | Add `<tool>.prompt.md` (~10 lines) + DB cases in scripts | Add a `Tool(...)` entry (~7 lines) |
| **Parallelism** | Must add `&` + `wait` plumbing | `ateam parallel :release_crawl/...` (built-in) | `Runner.run_many()` (built-in) |
| **Per-tool failure handling** | One `||` block at the call site | Dispatcher catches; or `ateam flow error` | `try/except` + `on_failure(tool)` |
| **Preview the assembled prompt** | `bash -x` to see the heredoc expansion | `ateam prompt :release_crawl/claude_code --preview` | `runner.preview(name, ctx)` |
| **Type safety** | None | None | Yes (Tool dataclass, Ctx typing) |
| **Skip vs error vs success** | Conflated (exit codes) | `ateam flow skip/error` | `Flow("skip"/"error"/"continue")` |

## Evaluating the "add a pre-flight check" change

Concrete request: add an algorithmic step that queries the source URL, finds
the latest version, compares with the DB, and skips the LLM run if nothing new.

### In shell (~15-20 lines added)

```bash
function check-new {
  local project=$1
  local url=$2
  local latest_remote
  latest_remote="$(curl -fsSL "$url" | grep -Eo 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
  local latest_known
  latest_known="$(ccdb view --project "$project" --sort -updated --limit 1 --format short)"
  [ -n "$latest_remote" ] && [ "$latest_remote" = "$latest_known" ] && return 0
  return 1
}

function crawl-tool {
  ...
  # NEW: prepend before the prompt heredoc
  if check-new "$project" "${primary_url}"; then
    echo "SKIPPED: $tool_name — no new versions"
    return 0
  fi
  ...
}
```

- **Cost:** ~15 lines + a positional argument for the primary URL.
- **Friction:** medium — the check function is fine on its own, but inserting
  the early-return into `crawl-tool` means the existing `||` failure handler
  has to coexist with a separate "no work" path. Easy to get wrong (does
  `set -e` interact badly with the conditional return?).

### In filesystem (~18 lines in one new script)

```bash
# scripts/check-new.sh (new file)
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  claude_code) project=ccversions; url=https://code.claude.com/docs/en/changelog ;;
  codex)       project=codex_versions; url=https://github.com/openai/codex/releases ;;
esac
latest_remote="$(curl -fsSL "$url" | grep -Eo 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
latest_known="$(mdtrack --db "${project}.sqlite" view --project "$project" \
                --sort -updated --limit 1 --format short | head -1)"
if [ -n "$latest_remote" ] && [ "$latest_remote" = "$latest_known" ]; then
  ateam flow skip --reason "no new $1 versions"
fi
```

And one frontmatter entry on `_pre.intro.md`:

```yaml
---
pre_exec:
  - ./scripts/check-new.sh {{prompt.name}}
---
```

- **Cost:** ~18 lines for the new script + ~3-line frontmatter on
  `_pre.intro.md`.
- **Friction:** low — the new script is fully isolated, testable from the
  command line, and the framework handles "skip" semantics. The dispatcher
  doesn't change at all. New tools can drop into `check-new.sh` with one more
  `case` arm.

### In Python (~10 lines + reuse `SkipIf`)

```python
def has_new_version(ctx: Ctx) -> bool:
    t: Tool = ctx.data["tool"]
    try:
        import urllib.request, re
        body = urllib.request.urlopen(t.latest_url, timeout=10).read().decode()
        m = re.search(r"v?(\d+\.\d+\.\d+)", body)
        remote = m.group(1) if m else None
    except Exception:
        return True
    return remote is None or remote != latest_known(t).lstrip("v")
```

And one entry in `pre_exec` (already in the example above):

```python
pre_exec=[SkipIf(lambda ctx: not has_new_version(ctx),
                 lambda ctx: f"{ctx.data['tool'].display}: no new versions")]
```

- **Cost:** ~10 lines for the predicate + 2 lines for the `SkipIf` wiring.
- **Friction:** lowest — `SkipIf` is exactly the abstraction for this. The
  function gets a typed `Tool` out of `ctx.data`. Testing the predicate is a
  unit test of one function. The `Tool` dataclass already has `latest_url`.

## Verdict per dimension

### Lines of code

- Smallest: **shell** (~127 lines), single file.
- Tied: **filesystem** (~126 lines across 10 files).
- Largest: **Python** (~170 lines for the script, ~300 if you count the
  one-time framework cost).

For a one-off workflow like this, shell wins on raw size. The Python framework
becomes worth it only when amortized across 2+ workflows.

### Readability

- **Shell**: easy to find but hard to skim — prompt text is buried in a
  heredoc inside a bash function, escaped where it interpolates with `$()`,
  and the failure block uses `&&\` chains that read as one long expression.
- **Filesystem**: easiest to skim per file. Each markdown file is a
  self-contained piece you can review in isolation. The trade-off is that
  understanding the *assembled* prompt requires `ateam prompt --preview` or
  reading the dispatcher + 3 markdown files together.
- **Python**: most readable as code, least readable as prompt content. The
  prompt text is a triple-quoted string; you can syntax-highlight it but not
  preview-render it without invoking the runner. Type signatures and named
  helpers make control flow easy to follow.

### Ease of changing

**Shell** is straightforward to modify when the change fits the existing
shape (add a tool, change a URL). It gets ugly when the change introduces a
new shape (early return, parallelism, structured skip vs error). Bash quoting
and `set -e` interactions are real hazards.

**Filesystem** is easiest for prompt edits (open the right `.md`) and for
adding pre/post steps (drop a script in `scripts/`, add a `pre_exec:` line to
the frontmatter of `_pre.intro.md`). It's awkward when the change is purely
algorithmic and doesn't touch prompts — you end up writing the same shell as
the original anyway, just in a different directory.

**Python** is best when the change is algorithmic and structured —
`SkipIf` / `Tool` dataclass / runner extension all land cleanly. It's worst
when you want to share content across workflows: there's no equivalent of
the filesystem's `_post.format.md` being included by multiple prompts without
either copy-pasting the string or factoring it into a shared module.

### The pre-flight check specifically

Of the three:
1. **Python**: cleanest. `SkipIf(callable, reason_callable)` is exactly the
   abstraction. The predicate is one function, testable, typed.
2. **Filesystem**: cleanest separation. The check is a standalone script you
   can run from the shell to test; `ateam flow skip` signals back to the
   runner without touching prompts or dispatcher.
3. **Shell**: requires inserting a guard inside `crawl-tool`'s body, with
   careful attention to `set -e` interactions. Works, but accumulates cruft.

## Honest take

For *this* workflow specifically:

- The shell script is already very close to the right shape. ~127 lines,
  one file, easy to understand. Migrating to either of the other approaches
  is more effort than warranted unless you expect:
  - More tools beyond Claude Code / Codex,
  - More pre/post steps (the pre-flight check is exactly the kind of thing
    that tips the balance),
  - Wanting to parallelize, preview, or audit runs through `ateam ps`.

- The **filesystem** approach shines if the *prompt content* will grow or get
  edited often — markdown files diff better, review better, and the assembled
  output is previewable. The 10-file count looks bad on paper but each file
  is tiny and self-contained.

- The **Python API** is the right choice when the workflow has genuine
  algorithmic state — multiple tools with structured config, pre/post hooks
  that need types and testing, parallel batches with skip-vs-error
  distinctions. The metaproject example earned it; this crawl example
  doesn't quite, but the pre-flight-check requirement is the first hint that
  it might.

A reasonable migration path: stay on shell until either (a) you want
parallel-with-skip semantics, or (b) the prompt content needs to be shared
with another workflow. Then move to the filesystem direction (for shared
prompt content) or the Python direction (for shared algorithmic structure).
The choice is about what *kind* of duplication is worth eliminating.
