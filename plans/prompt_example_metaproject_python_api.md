# Prompt Example: `metaproject.py` As A Small Python API

Source example: `../metaproject/metaproject.py`

This explores a different direction than the prompt-filesystem proposal: keep a
small Python layer for workflows that are inherently programmable, but make the
programmability structured. Split into two pieces:

- **Framework** — reusable, knows nothing about metaproject. Defines the
  `Ctx` / `Flow` / `ExecPrompt` / `ExecAction` / `PromptBundle` / `Runner`
  primitives, the parallel runner, and a few generic exec-action helpers
  (`SkipIf`, `EnsureParents`, `BackupFiles`).
- **Metaproject** — every line that's specific to discover/audit/fix/verify,
  the five scopes, the `reports/` layout, and the CLI. Built on top of the
  framework.

The core distinction stays the same:

| Concept | Meaning |
|---|---|
| `ExecPrompt` | Runs during prompt assembly. Its output is part of the prompt. Runs during preview. |
| `ExecAction` | Runs before or after the agent. Output not in the prompt. Does NOT run during preview. |
| `PromptBundle` | One executable agent unit: prompt parts plus pre/post exec actions. |
| `Runner` | Registry + executor. Handles preview, serial run, parallel run. |

## The framework (`ateam_runner.py`)

Generic. Reusable for any agent workflow.

```python
"""ateam_runner.py — generic prompt-bundle runner.

A small Python framework for bundling a prompt with optional pre/post exec
actions. Bundles can be previewed (renders the prompt, no actions) or run
(executes the agent via `ateam exec`, with pre/post actions firing around the
agent call). Multiple bundles run in parallel via `run_many`.
"""
from __future__ import annotations

import concurrent.futures
import shutil
import subprocess
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Any, Callable, Literal, Protocol


# === Flow control ===

FlowState = Literal["continue", "skip", "error"]


@dataclass(frozen=True)
class Flow:
    state: FlowState = "continue"
    reason: str = ""


# === Context ===

@dataclass(frozen=True)
class Ctx:
    """Per-invocation context. Workflows stash their own state in `data`."""
    work_dir: Path
    role: str = ""          # passed to `ateam exec --role`
    action: str = ""        # passed to `ateam exec --action`
    force: bool = False
    dry_run: bool = False
    data: dict[str, Any] = field(default_factory=dict)


# === Protocols ===

class ExecPrompt(Protocol):
    """Produces text inserted into the prompt. Runs during preview."""
    def render(self, ctx: Ctx) -> str: ...


class ExecAction(Protocol):
    """Runs before or after the agent. Output is NOT in the prompt."""
    def run(self, ctx: Ctx) -> Flow: ...


PromptPart = str | ExecPrompt


# === Adapters — lift plain functions into protocols ===

@dataclass(frozen=True)
class PromptFn:
    fn: Callable[[Ctx], str]
    def render(self, ctx: Ctx) -> str:
        return self.fn(ctx)


@dataclass(frozen=True)
class ActionFn:
    fn: Callable[[Ctx], Flow]
    def run(self, ctx: Ctx) -> Flow:
        return self.fn(ctx)


# === Common exec-action helpers ===

@dataclass(frozen=True)
class SkipIf:
    predicate: Callable[[Ctx], bool]
    reason: Callable[[Ctx], str]
    def run(self, ctx: Ctx) -> Flow:
        return Flow("skip", self.reason(ctx)) if self.predicate(ctx) else Flow()


@dataclass(frozen=True)
class EnsureParents:
    paths: Callable[[Ctx], list[Path]]
    def run(self, ctx: Ctx) -> Flow:
        for p in self.paths(ctx):
            p.parent.mkdir(parents=True, exist_ok=True)
        return Flow()


@dataclass(frozen=True)
class BackupFiles:
    paths: Callable[[Ctx], list[Path]]
    def run(self, ctx: Ctx) -> Flow:
        ts = datetime.now().strftime("%Y-%m-%d_%H%M%S")
        for p in self.paths(ctx):
            if p.exists():
                shutil.copy2(p, p.with_name(f"{p.stem}.{ts}{p.suffix}"))
        return Flow()


# === Bundle and result ===

@dataclass(frozen=True)
class PromptBundle:
    name: str
    prompt: list[PromptPart]
    pre_exec: list[ExecAction] = field(default_factory=list)
    post_exec: list[ExecAction] = field(default_factory=list)


@dataclass(frozen=True)
class Result:
    bundle: str
    flow: Flow


# === Runner ===

def render_prompt(bundle: PromptBundle, ctx: Ctx) -> str:
    chunks = [p if isinstance(p, str) else p.render(ctx) for p in bundle.prompt]
    return "\n".join(c.rstrip() for c in chunks if c.strip()) + "\n"


class Runner:
    def __init__(self) -> None:
        self.bundles: dict[str, PromptBundle] = {}

    def add(self, bundle: PromptBundle) -> None:
        if bundle.name in self.bundles:
            raise ValueError(f"duplicate bundle: {bundle.name}")
        self.bundles[bundle.name] = bundle

    def preview(self, name: str, ctx: Ctx) -> str:
        return render_prompt(self.bundles[name], ctx)

    def run(self, name: str, ctx: Ctx) -> Result:
        bundle = self.bundles[name]
        prompt = render_prompt(bundle, ctx)
        if ctx.dry_run:
            print(f"---- PROMPT ({name}, work-dir: {ctx.work_dir}) ----")
            print(prompt)
            print("---- END PROMPT ----")
            return Result(name, Flow())
        for action in bundle.pre_exec:
            flow = action.run(ctx)
            if flow.state != "continue":
                return Result(name, flow)
        subprocess.run(
            ["ateam", "exec",
             "--work-dir", str(ctx.work_dir),
             "--action", ctx.action or name,
             "--role", ctx.role or name],
            input=prompt, text=True, check=True,
        )
        for action in bundle.post_exec:
            flow = action.run(ctx)
            if flow.state == "error":
                return Result(name, flow)
        return Result(name, Flow())

    def run_many(self, items: list[tuple[str, Ctx]],
                 *, workers: int | None = None) -> list[Result]:
        if len(items) == 1 or all(c.dry_run for _, c in items):
            return [self.run(n, c) for n, c in items]
        max_workers = workers or min(len(items), 5)
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as pool:
            futures = [pool.submit(self.run, n, c) for n, c in items]
            return [f.result() for f in concurrent.futures.as_completed(futures)]
```

**Framework size: ~130 lines** (including blank lines and comments).

## The metaproject script

Everything below is specific to discover/audit/fix/verify, the five scopes, and
the `reports/` layout. The framework above stays untouched.

```python
#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["click>=8.1"]
# ///
"""metaproject.py — discover/audit/fix/verify across projects."""
from __future__ import annotations

import subprocess
import tomllib
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Callable

import click

from ateam_runner import (
    Ctx, Flow, PromptBundle, Runner, PromptFn, SkipIf,
    EnsureParents, BackupFiles,
)

SCRIPT_DIR = Path(__file__).resolve().parent
BASE_REPORTS = SCRIPT_DIR / "reports"
ALL_SCOPES = ["claude", "gitconfig", "build", "test", "docker"]
PIPELINE = ["discover", "audit", "fix", "verify"]
ACTION_CHAIN: dict[str, list[str]] = {
    "discover": ["discover"], "audit": ["audit"], "fix": ["fix"], "verify": ["verify"],
    "discover+": PIPELINE, "audit+": ["audit", "fix", "verify"],
    "fix+": ["fix", "verify"], "all": PIPELINE,
}
META_ACTIONS = ["projects", "prompt", "reports"]


# === Target & per-run context ===

@dataclass(frozen=True)
class Target:
    label: str
    path: Path

    @classmethod
    def parse(cls, raw: str) -> "Target":
        if ":" not in raw:
            raise click.BadParameter(f"target {raw!r} must be LABEL:PATH")
        label, path = raw.split(":", 1)
        if not label or "/" in label or ".." in label:
            raise click.BadParameter(f"label {label!r} must be safe (no '/' or '..')")
        if not path:
            data = read_files_toml(label)
            meta = (data or {}).get("meta") or {}
            path = meta.get("path_canonical") or meta.get("path_used") or ""
            if not path:
                raise click.BadParameter(f"label {label!r} not yet discovered")
        p = Path(path).expanduser()
        if not p.is_dir():
            raise click.BadParameter(f"path {path!r} is not a directory")
        check = subprocess.run(["git", "rev-parse", "--is-inside-work-tree"],
                               cwd=p, capture_output=True, text=True)
        if check.returncode != 0 or check.stdout.strip() != "true":
            raise click.BadParameter(f"path {path!r} not inside a git work tree")
        return cls(label, p)


def ctx_for(target: Target, bundle: str, *, force: bool, dry_run: bool) -> Ctx:
    action, _, scope = bundle.partition("/")
    return Ctx(
        work_dir=target.path,
        action=action,
        role=f"{scope}/{target.label}" if scope else target.label,
        force=force, dry_run=dry_run,
        data={"label": target.label, "path": target.path, "target": target,
              "action": action, "scope": scope or None},
    )


# === Output layout ===

def report_path(action: str, scope: str, label: str) -> Path:
    return BASE_REPORTS / label / scope / f"{action}.md"

def overview_path(label: str) -> Path:
    return BASE_REPORTS / label / "overview.md"

def files_toml_path(label: str) -> Path:
    return BASE_REPORTS / label / "files.toml"

def tests_toml_path(label: str) -> Path:
    return BASE_REPORTS / label / "test" / "tests.toml"

def outputs_for(ctx: Ctx) -> list[Path]:
    action, scope, label = ctx.data["action"], ctx.data["scope"], ctx.data["label"]
    if action == "discover":
        return [overview_path(label), files_toml_path(label)]
    paths = [report_path(action, scope, label)]
    if action == "audit" and scope == "test":
        paths.append(tests_toml_path(label))
    return paths


# === Git + manifest helpers ===

def git_state(p: Path) -> tuple[str, str]:
    branch = subprocess.run(["git", "symbolic-ref", "--short", "HEAD"],
                            cwd=p, capture_output=True, text=True).stdout.strip() or "DETACHED"
    commit = subprocess.run(["git", "rev-parse", "HEAD"],
                            cwd=p, capture_output=True, text=True).stdout.strip()
    return branch, commit

def read_files_toml(label: str) -> dict | None:
    p = files_toml_path(label)
    return tomllib.loads(p.read_text()) if p.exists() else None

def scope_files(t: Target, data: dict, scope: str) -> list[Path]:
    return [t.path / r for r in (data.get("files", {}).get(scope) or [])]

def all_tracked_files(t: Target, data: dict) -> list[Path]:
    return [t.path / r for fs in data.get("files", {}).values() for r in (fs or [])]

def files_newer_than(ref: Path, files: list[Path]) -> list[Path]:
    if not ref.exists():
        return [f for f in files if f.exists()]
    rt = ref.stat().st_mtime
    return [f for f in files if f.exists() and f.stat().st_mtime > rt]

def read_or(p: Path, missing: str) -> str:
    if not p.exists():
        return missing
    t = p.read_text()
    return t if t.strip() else missing


# === Skip predicates ===

def discover_is_fresh(ctx: Ctx) -> bool:
    if ctx.force:
        return False
    label = ctx.data["label"]
    data = read_files_toml(label)
    if data is None:
        return False
    return not files_newer_than(files_toml_path(label),
                                all_tracked_files(ctx.data["target"], data))

def audit_is_fresh(ctx: Ctx) -> bool:
    if ctx.force:
        return False
    label, scope = ctx.data["label"], ctx.data["scope"]
    out = report_path("audit", scope, label)
    data = read_files_toml(label)
    if data is None or not out.exists():
        return False
    files = scope_files(ctx.data["target"], data, scope)
    return True if not files else not files_newer_than(out, files)

def followup_is_fresh(parent: str, child: str) -> Callable[[Ctx], bool]:
    def check(ctx: Ctx) -> bool:
        if ctx.force:
            return False
        label, scope = ctx.data["label"], ctx.data["scope"]
        p, c = report_path(parent, scope, label), report_path(child, scope, label)
        return c.exists() and p.exists() and c.stat().st_mtime >= p.stat().st_mtime
    return check

def skip_reason(ctx: Ctx) -> str:
    label, scope, action = ctx.data["label"], ctx.data["scope"], ctx.data["action"]
    return f"{action}/{scope}/{label}: up to date" if scope else f"{action}/{label}: up to date"


# === Prompt builders ===

def intro(ctx: Ctx) -> str:
    scope = ctx.data["scope"]
    sx = f" (scope {scope!r})" if scope else ""
    return (f"You are running {ctx.data['action']!r}{sx} for project "
            f"{ctx.data['label']!r} at '{ctx.data['path']}'.\n"
            "Keep your focus very narrow to the instruction below.")

def footer(ctx: Ctx, out: Path) -> str:
    return f"\nSave your report on project '{ctx.data['label']}' at: {out}\n"


def discover_prompt(ctx: Ctx) -> str:
    label, target = ctx.data["label"], ctx.data["target"]
    data = read_files_toml(label)
    branch, commit = git_state(target.path)
    now = datetime.now().strftime("%Y-%m-%dT%H:%M:%S")
    op, fp = overview_path(label), files_toml_path(label)
    meta = (f'[meta]\ngit_branch       = "{branch}"\n'
            f'git_commit_hash  = "{commit}"\ndiscovered_at    = "{now}"\n'
            f'path_used        = "{target.path.absolute()}"\n'
            f'path_canonical   = "{target.path.resolve()}"\n')
    if data is None:
        body = f"""{intro(ctx)}

Goals — produce two files:
1. Overview: '{op}'
2. TOML manifest: '{fp}'

The overview is a concise narrative covering:
* what the project is, language/stack
* how to build, or "no build step"
* how to run tests by category, or "no tests"
* dev-environment setup (Docker, devcontainer, etc.) if any
* anything non-obvious for troubleshooting

The TOML must use exactly this structure:

{meta}
[files]
claude    = ["CLAUDE.md"]            # use [] if absent
gitconfig = [".git/config"]
build     = ["Makefile", "..."]
test      = ["..."]
docker    = ["Dockerfile", "..."]

Paths are relative to '{target.path}'. List the smallest set of files an auditor
would need to read to evaluate each scope. Use [] for scopes with nothing relevant.

Copy the [meta] block verbatim."""
    else:
        changed = files_newer_than(fp, all_tracked_files(target, data))
        changed_list = "\n".join(f"  - {p.relative_to(target.path)}" for p in changed)
        body = f"""{intro(ctx)}

Incremental update. Existing artifacts:
* Overview: '{op}'
* Manifest: '{fp}'

Files changed since last discover:
{changed_list}

Inspect ONLY these files. Minimum-effort update:
* Update '{op}' only if changes meaningfully affect it.
* Update '{fp}' [files] sections only if the relevant lists changed.
* Always replace the [meta] block with:

{meta}"""
    return body + footer(ctx, op)


SCOPE_AUDIT: dict[str, str] = {
    "claude": """\
Recommend improvements to CLAUDE.md in '{path}' so it clearly documents:
* git usage, if non-standard
* how to build, or N/A
* how to run tests, by category if multiple
* common troubleshooting steps
Be specific: cite missing sections, vague instructions, or commands that
do not match what the overview describes.""",
    "gitconfig": """\
Verify git identity for '{label}' at '{path}':
* user.name is set, locally or globally — note which
* user.email is set, locally or globally — note which
Report what is configured. Any identity is acceptable for now.""",
    "build": """\
Audit the build story for '{label}'. Flag:
* commands that do not work as documented
* missing prerequisites or env vars
* inconsistencies between overview and actual project files
If the stack does not need a build step, state that and stop.""",
    "test": """\
Audit the test commands for '{label}' in 2 parts.

### Commands that exist today
Only document what can be run today:
* the test framework(s) in use
* the command for each category, and the directory it should be run from
* any setup required to run tests (services, fixtures, env vars)
If no tests exist, report that explicitly.

Write a structured toml file in '{tests_toml}':

    [tests]
    fast=QUICK_VERIFICATION_COMMAND
    all=RUN_ABSOLUTELY_ALL_TESTS
    AREA_X=COMMAND_X
    AREA_Y=COMMAND_Y

Replace placeholders with actual commands. Areas are things like 'backend',
'frontend', 'cli', or 'benchmark'.

### How complete is the test story
Flag:
* test commands that do not match what the project supports
* missing categories (e.g. e2e tests with no documented runner)
* broken or skipped suites worth surfacing""",
    "docker": """\
Audit the dev-environment-in-Docker setup for '{label}'. Flag:
* broken Dockerfile / compose / devcontainer
* commands that do not match what is documented
If no Docker setup exists, recommend whether one would be valuable given
the project's stack, and stop if not.""",
}


def audit_prompt(ctx: Ctx) -> str:
    label, scope, target = ctx.data["label"], ctx.data["scope"], ctx.data["target"]
    out = report_path("audit", scope, label)
    overview = read_or(overview_path(label), "(no overview yet)")
    data = read_files_toml(label)
    if data is None:
        changes = "(no files.toml — discover has not run)"
    else:
        tracked = scope_files(target, data, scope)
        if not tracked:
            changes = f"(no files tracked for scope {scope!r})"
        else:
            op = overview_path(label)
            ch = files_newer_than(op, tracked) if op.exists() else tracked
            changes = ("(nothing has changed)" if not ch
                       else "\n".join(f"- `{p.relative_to(target.path)}`" for p in ch))
    body = SCOPE_AUDIT[scope].format(path=target.path, label=label,
                                     tests_toml=tests_toml_path(label))
    return f"""# Context

# Project Overview

{overview}

# File changes since overview was produced

{changes}

# Previous report

{read_or(out, "(none)")}

# Action

{intro(ctx)}
Use the Project Overview above for context. Do NOT redo discovery.

{body}
{footer(ctx, out)}"""


def fix_prompt(ctx: Ctx) -> str:
    label, scope = ctx.data["label"], ctx.data["scope"]
    audit_out = report_path("audit", scope, label)
    out = report_path("fix", scope, label)
    return f"""{intro(ctx)}
Look at '{audit_out}' if it exists; if not, you are done and your last
message must be exactly: "no audit items to implement".
Otherwise make the recommended changes inside '{ctx.data['path']}'.
{footer(ctx, out)}"""


def verify_prompt(ctx: Ctx) -> str:
    label, scope = ctx.data["label"], ctx.data["scope"]
    fix_out = report_path("fix", scope, label)
    out = report_path("verify", scope, label)
    return f"""{intro(ctx)}
Look at '{fix_out}' — it documents improvements for scope '{scope}'.
Go to '{ctx.data['path']}' and try to follow the steps / run the commands.
If there are issues, fix them and update '{fix_out}' to be accurate. For any
change in that file, note that you made changes, why, and what they are.
{footer(ctx, out)}"""


# === Bundle registration ===

def build_runner() -> Runner:
    r = Runner()
    setup = [EnsureParents(outputs_for), BackupFiles(outputs_for)]
    r.add(PromptBundle(
        name="discover",
        prompt=[PromptFn(discover_prompt)],
        pre_exec=[SkipIf(discover_is_fresh, skip_reason), *setup],
    ))
    for scope in ALL_SCOPES:
        r.add(PromptBundle(f"audit/{scope}", [PromptFn(audit_prompt)],
                           [SkipIf(audit_is_fresh, skip_reason), *setup]))
        r.add(PromptBundle(f"fix/{scope}", [PromptFn(fix_prompt)],
                           [SkipIf(followup_is_fresh("audit", "fix"), skip_reason), *setup]))
        r.add(PromptBundle(f"verify/{scope}", [PromptFn(verify_prompt)],
                           [SkipIf(followup_is_fresh("fix", "verify"), skip_reason), *setup]))
    return r


# === Init / list_projects / list_reports ===

def init(verbose: bool) -> None:
    if not (SCRIPT_DIR / ".ateam").is_dir():
        r = subprocess.run(["ateam", "init"], cwd=SCRIPT_DIR, capture_output=True, text=True)
        if r.stderr:
            click.echo(r.stderr, err=True, nl=False)
        if r.returncode != 0:
            raise click.ClickException(f"ateam init failed (exit {r.returncode})")
    env = subprocess.run(["ateam", "--project", str(SCRIPT_DIR), "env"],
                         capture_output=True, text=True)
    if verbose or env.returncode != 0:
        if env.stdout:
            click.echo(env.stdout, nl=False)
        if env.stderr:
            click.echo(env.stderr, err=True, nl=False)
    if env.returncode != 0:
        raise click.ClickException(f"ateam env failed (exit {env.returncode})")
    BASE_REPORTS.mkdir(parents=True, exist_ok=True)


def list_projects(short: bool) -> None:
    if not BASE_REPORTS.exists():
        return
    cands = sorted(c for c in BASE_REPORTS.iterdir()
                   if c.is_dir() and (c / "files.toml").exists())
    if short:
        for c in cands:
            click.echo(c.name)
        return
    rows: list[tuple[str, ...]] = []
    for c in cands:
        try:
            data = tomllib.loads((c / "files.toml").read_text())
        except tomllib.TOMLDecodeError as e:
            rows.append((c.name, f"(malformed: {e})", "", "", ""))
            continue
        m = data.get("meta") or {}
        path = m.get("path_canonical") or m.get("path_used") or "?"
        branch = m.get("git_branch", "?")
        commit = (m.get("git_commit_hash") or "")[:8] or "?"
        ts = m.get("discovered_at", "?")
        scopes = sorted(s for s, fs in (data.get("files") or {}).items() if fs)
        rows.append((c.name, path, f"[{branch}@{commit}]", ts, ", ".join(scopes) or "-"))
    if not rows:
        return
    widths = [max(len(r[i]) for r in rows) for i in range(len(rows[0]) - 1)]
    for r in rows:
        head = "  ".join(c.ljust(w) for c, w in zip(r[:-1], widths))
        click.echo(f"{head}  {r[-1]}")


def list_reports() -> None:
    if not BASE_REPORTS.exists():
        return
    click.echo("project,scope,action,relative_path")
    wanted = {f"{a}.md" for a in ("audit", "fix", "verify")}
    for pd in sorted(BASE_REPORTS.iterdir()):
        if not pd.is_dir():
            continue
        for sd in sorted(pd.iterdir()):
            if not sd.is_dir():
                continue
            for f in sorted(sd.iterdir()):
                if f.is_file() and f.name in wanted:
                    click.echo(f"{pd.name},{sd.name},{f.stem},{f.relative_to(SCRIPT_DIR)}")


# === Pipeline ===

def run_pipeline(runner: Runner, target: Target, stages: list[str], scopes: list[str],
                 *, force: bool, dry_run: bool) -> None:
    if any(s in stages for s in ("audit", "fix", "verify")) and "discover" not in stages:
        stages = ["discover"] + stages
    for stage in stages:
        names = ["discover"] if stage == "discover" else [f"{stage}/{s}" for s in scopes]
        items = [(n, ctx_for(target, n, force=force, dry_run=dry_run)) for n in names]
        for r in sorted(runner.run_many(items), key=lambda r: r.bundle):
            if r.flow.state == "skip":
                click.echo(f"= {r.bundle}/{target.label}: {r.flow.reason}")
            else:
                click.echo(f"→ {r.bundle}/{target.label}: done")


# === CLI ===

@click.command(context_settings={"help_option_names": ["-h", "--help"]})
@click.argument("action", type=click.Choice(list(ACTION_CHAIN.keys()) + META_ACTIONS))
@click.argument("targets", nargs=-1, metavar="[TARGET...]")
@click.option("--dry-run", is_flag=True)
@click.option("--scope", default="all", type=click.Choice(["all", *ALL_SCOPES]))
@click.option("--force", is_flag=True)
@click.option("--short", is_flag=True)
@click.option("--for", "for_action", type=click.Choice(PIPELINE), default="audit")
@click.option("-v", "--verbose", is_flag=True)
def main(action, targets, dry_run, scope, force, short, for_action, verbose):
    """discover/audit/fix/verify across projects."""
    runner = build_runner()

    if action == "projects":
        if targets: raise click.UsageError("'projects' takes no targets")
        list_projects(short); return
    if action == "reports":
        if targets: raise click.UsageError("'reports' takes no targets")
        list_reports(); return
    if action == "prompt":
        if len(targets) != 1:
            raise click.UsageError("'prompt' requires exactly one TARGET")
        t = Target.parse(targets[0])
        bundle = "discover" if for_action == "discover" else f"{for_action}/{scope}"
        if for_action != "discover" and scope == "all":
            raise click.UsageError(f"'prompt --for {for_action}' requires --scope")
        click.echo(runner.preview(bundle, ctx_for(t, bundle, force=force, dry_run=True)))
        return

    if not targets:
        raise click.UsageError("at least one TARGET is required")
    parsed = [Target.parse(t) for t in targets]
    stages = list(ACTION_CHAIN[action])
    scopes = ALL_SCOPES if scope == "all" else [scope]
    if not dry_run:
        init(verbose)
    for target in parsed:
        run_pipeline(runner, target, stages, scopes, force=force, dry_run=dry_run)


if __name__ == "__main__":
    main()
```

## Line count

| File | Lines | Of which |
|---|---:|---|
| `ateam_runner.py` (framework) | ~130 | Reusable for any agent workflow |
| `metaproject.py` (this workflow only) | ~360 | ~210 lines of Python, ~150 lines of literal prompt text in multi-line strings |
| **Total** | **~490** | Original `metaproject.py` was 754 lines |

The reduction (754 → 490) is real but moderate. The bigger win is structural:

- The framework (~130 lines) is now reusable. A second workflow in this style
  would only pay for its own ~360-line script.
- Of the 360 metaproject lines, ~150 are literal prompt text inside multi-line
  strings — that's prompt *content*, not control flow. Strip those and the
  Python control flow + helpers fit in ~210 lines.
- Each piece has one job:
  - `ateam_runner.py`: prompt-bundle execution, parallelism, skip/backup helpers
  - `metaproject.py`: target parsing, paths, prompt text, skip predicates, CLI
  - prompt text: lives in clearly-marked multi-line strings inside the script

## Why this is better than the current script

The current script already has the right ingredients, but they are not first
class. This version makes them first class:

| Current `metaproject.py` | Python API version |
|---|---|
| `build_audit_prompt()` manually assembles every section | `PromptBundle(prompt=[...])` names the prompt parts |
| `run_audit()` mixes skip, backup, prompt, and execution | `pre_exec`, `prompt`, `Runner.run()` separate them |
| Scope functions registered separately from run behavior | Each scope becomes `audit/<scope>`, `fix/<scope>`, `verify/<scope>` bundles |
| Scoped work is serial | Scoped bundles run through `Runner.run_many()` in parallel |
| Preview is separate ad hoc code | `Runner.preview()` renders the same bundle prompt without actions |

Per-target execution order with this version:

```
for each target:
  discover
  audit/claude, audit/gitconfig, audit/build, audit/test, audit/docker  in parallel
  fix/claude,   fix/gitconfig,   fix/build,   fix/test,   fix/docker    in parallel
  verify/claude, verify/gitconfig, verify/build, verify/test, verify/docker in parallel
```

Overview generation stays deterministic and first; the slow serial loop over
independent scopes goes away.

## Open design questions

1. `PromptBundle` is a good name for one executable agent unit; `Runner` might
   be too generic. `PromptRunner` or `AgentRunner` would be clearer in a larger
   codebase.
2. The generic API should probably standardize result capture from `ateam exec`
   instead of assuming the agent writes directly to the instructed output path.
3. Parallel execution needs a configurable worker limit. Five scopes maps well
   to this example, but generic workflows need budget and concurrency controls.
4. If this became part of ateam, the Python API should expose the same preview
   and process metadata that the CLI exposes — not hide it behind subprocesses.
