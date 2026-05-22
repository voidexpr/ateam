#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""crawl.py — track new tool releases via release notes.

Design artifact, not in-tree. Companion to plans/ateam.py.

Tighter workload than metaproject: one workflow, two tool variants, prompt
mostly static. The prompt is assembled entirely in Python (PromptFn) — no
external prompt files for this example since the content is short and
heavily computed.
"""
from __future__ import annotations

import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from ateam import Ctx, PromptBundle, PromptFn, Runner, SkipIf

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
    if not t.db_file.exists():
        return ""
    out = mdtrack(t.db_file, "view", "--project", t.project,
                  "--sort", "-updated", "--limit", "1", "--format", "short")
    return out.strip().split("\n", 1)[0]


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
