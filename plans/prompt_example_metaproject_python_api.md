# Prompt Example: `metaproject.py` As A Small Python API

Source example: `../metaproject/metaproject.py`

This explores a different direction than the prompt-filesystem proposal: keep a
small Python layer for workflows that are inherently programmable, but make the
programmability structured. The core distinction stays the same:

| Concept | Meaning |
|---|---|
| `ExecPrompt` | Runs during prompt assembly. Its output is included in the prompt. Runs during preview. |
| `ExecAction` | Runs before or after the agent. Its output is not included in the prompt. Does not run during preview. |
| `PromptBundle` | One executable agent unit: prompt parts plus pre/post exec actions. |
| `Runner` | Registry and executor for bundles. Handles preview, run, and parallel run. |

This is probably a better fit for `metaproject.py` than pure prompt files. The
workflow has real algorithmic state: manifests, mtimes, target parsing,
incremental discovery, fan-out, and report listings. The API below keeps that in
Python while preventing it from becoming one long string-building script.

## Minimal API Shape

```python
class ExecPrompt(Protocol):
    def render(self, ctx: Ctx) -> str: ...

class ExecAction(Protocol):
    def run(self, ctx: Ctx) -> Flow: ...

@dataclass
class PromptBundle:
    name: str
    prompt: list[str | ExecPrompt]
    pre_exec: list[ExecAction] = field(default_factory=list)
    post_exec: list[ExecAction] = field(default_factory=list)

class Runner:
    def add(self, bundle: PromptBundle) -> None: ...
    def preview(self, name: str, target: Target, *, scope: str | None = None) -> str: ...
    def run(self, name: str, target: Target, *, force: bool = False) -> Result: ...
    def run_many(self, names: list[str], target: Target, *, force: bool = False) -> list[Result]: ...
```

The improvement over the current script is not that Python disappears. It is
that each piece gets a sharper type:

- prompt text and deterministic prompt context are `ExecPrompt`
- skip checks, backups, and promotion are `ExecAction`
- `audit/test`, `fix/docker`, and `discover` are named `PromptBundle`s
- the CLI is mostly target parsing and pipeline selection

## Reference Implementation

This implementation keeps the behavior of `metaproject.py`, but changes scoped
stages from serial execution to parallel execution. For each target, `discover`
always runs before `audit`, `fix`, or `verify`, because the overview and
`files.toml` are prerequisites for scoped work.

```python
#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["click>=8.1"]
# ///
from __future__ import annotations

import concurrent.futures
import shutil
import subprocess
import tomllib
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Callable, Literal, Protocol

import click


# === Project constants ===

SCRIPT_DIR = Path(__file__).resolve().parent
BASE_DIR = SCRIPT_DIR
BASE_REPORTS = BASE_DIR / "reports"

ALL_SCOPES = ["claude", "gitconfig", "build", "test", "docker"]
PIPELINE = ["discover", "audit", "fix", "verify"]

ACTION_CHAIN: dict[str, list[str]] = {
    "discover": ["discover"],
    "audit": ["audit"],
    "fix": ["fix"],
    "verify": ["verify"],
    "discover+": PIPELINE,
    "audit+": ["audit", "fix", "verify"],
    "fix+": ["fix", "verify"],
    "all": PIPELINE,
}

META_ACTIONS = ["projects", "prompt", "reports"]
ALL_CLI_ACTIONS = list(ACTION_CHAIN.keys()) + META_ACTIONS


# === Output layout ===

def report_path(action: str, scope: str, project: str) -> Path:
    return BASE_REPORTS / project / scope / f"{action}.md"


def structured_report_path(action: str, scope: str, project: str, filename: str) -> Path:
    return BASE_REPORTS / project / scope / filename


def overview_path(project: str) -> Path:
    return BASE_REPORTS / project / "overview.md"


def files_toml_path(project: str) -> Path:
    return BASE_REPORTS / project / "files.toml"


# === Workflow data ===

@dataclass(frozen=True)
class Target:
    label: str
    path: Path

    @property
    def path_abs(self) -> Path:
        return self.path.absolute()

    @property
    def path_canonical(self) -> Path:
        return self.path.resolve()

    @classmethod
    def parse(cls, raw: str) -> "Target":
        if ":" not in raw:
            raise click.BadParameter(f"target {raw!r} must be in LABEL:PATH form")
        label, path = raw.split(":", 1)
        if not label or "/" in label or ".." in label:
            raise click.BadParameter(
                f"label {label!r} must be a safe directory name (no '/' or '..')"
            )
        if not path:
            data = read_files_toml(label)
            meta = (data or {}).get("meta") or {}
            path = meta.get("path_canonical") or meta.get("path_used") or ""
            if not path:
                raise click.BadParameter(
                    f"label {label!r} is not yet discovered; pass LABEL:PATH"
                )

        p = Path(path).expanduser()
        if not p.is_dir():
            raise click.BadParameter(
                f"project path {path!r} does not exist or is not a directory"
            )
        check = subprocess.run(
            ["git", "rev-parse", "--is-inside-work-tree"],
            cwd=p,
            capture_output=True,
            text=True,
        )
        if check.returncode != 0 or check.stdout.strip() != "true":
            raise click.BadParameter(
                f"project path {path!r} is not inside a git work tree"
            )
        return cls(label, p)


@dataclass(frozen=True)
class Ctx:
    target: Target
    action: str
    scope: str | None
    force: bool = False
    dry_run: bool = False
    base_dir: Path = BASE_DIR
    base_reports: Path = BASE_REPORTS

    @property
    def label(self) -> str:
        return self.target.label

    @property
    def path(self) -> Path:
        return self.target.path

    @property
    def bundle_name(self) -> str:
        return f"{self.action}/{self.scope}" if self.scope else self.action

    @property
    def role(self) -> str:
        return f"{self.scope}/{self.label}" if self.scope else self.label

    def output(self, filename: str) -> Path:
        if self.scope is None:
            return self.base_reports / self.label / filename
        return structured_report_path(self.action, self.scope, self.label, filename)


# === Minimal framework ===

FlowState = Literal["continue", "skip", "error"]


@dataclass(frozen=True)
class Flow:
    state: FlowState = "continue"
    reason: str = ""

    @classmethod
    def cont(cls) -> "Flow":
        return cls("continue")

    @classmethod
    def skip(cls, reason: str) -> "Flow":
        return cls("skip", reason)

    @classmethod
    def error(cls, reason: str) -> "Flow":
        return cls("error", reason)


@dataclass(frozen=True)
class Result:
    bundle: str
    target: str
    flow: Flow
    output: Path | None = None


class ExecPrompt(Protocol):
    def render(self, ctx: Ctx) -> str: ...


class ExecAction(Protocol):
    def run(self, ctx: Ctx) -> Flow: ...


PromptPart = str | ExecPrompt


@dataclass(frozen=True)
class ExecPromptFn:
    fn: Callable[[Ctx], str]

    def render(self, ctx: Ctx) -> str:
        return self.fn(ctx)


@dataclass(frozen=True)
class ReadFile:
    path: Callable[[Ctx], Path]
    missing: str = ""

    def render(self, ctx: Ctx) -> str:
        p = self.path(ctx)
        if not p.exists():
            return self.missing
        text = p.read_text()
        return text if text.strip() else self.missing


@dataclass(frozen=True)
class ExecActionFn:
    fn: Callable[[Ctx], Flow]

    def run(self, ctx: Ctx) -> Flow:
        return self.fn(ctx)


@dataclass(frozen=True)
class SkipIf:
    predicate: Callable[[Ctx], bool]
    reason: Callable[[Ctx], str]

    def run(self, ctx: Ctx) -> Flow:
        if self.predicate(ctx):
            return Flow.skip(self.reason(ctx))
        return Flow.cont()


@dataclass(frozen=True)
class EnsureParents:
    paths: Callable[[Ctx], list[Path]]

    def run(self, ctx: Ctx) -> Flow:
        for p in self.paths(ctx):
            p.parent.mkdir(parents=True, exist_ok=True)
        return Flow.cont()


@dataclass(frozen=True)
class BackupFiles:
    paths: Callable[[Ctx], list[Path]]

    def run(self, ctx: Ctx) -> Flow:
        ts = datetime.now().strftime("%Y-%m-%d_%H%M%S")
        for p in self.paths(ctx):
            if p.exists():
                shutil.copy2(p, p.with_name(f"{p.stem}.{ts}{p.suffix}"))
        return Flow.cont()


@dataclass(frozen=True)
class PromptBundle:
    name: str
    prompt: list[PromptPart]
    pre_exec: list[ExecAction] = field(default_factory=list)
    post_exec: list[ExecAction] = field(default_factory=list)
    output: Callable[[Ctx], Path | None] = lambda _ctx: None

    @property
    def action(self) -> str:
        return self.name.split("/", 1)[0]

    @property
    def scope(self) -> str | None:
        parts = self.name.split("/", 1)
        return parts[1] if len(parts) == 2 else None


class Runner:
    def __init__(self) -> None:
        self.bundles: dict[str, PromptBundle] = {}

    def add(self, bundle: PromptBundle) -> None:
        if bundle.name in self.bundles:
            raise ValueError(f"duplicate bundle: {bundle.name}")
        self.bundles[bundle.name] = bundle

    def ctx(
        self,
        name: str,
        target: Target,
        *,
        force: bool = False,
        dry_run: bool = False,
    ) -> Ctx:
        bundle = self.bundles[name]
        return Ctx(
            target=target,
            action=bundle.action,
            scope=bundle.scope,
            force=force,
            dry_run=dry_run,
        )

    def preview(self, name: str, target: Target, *, force: bool = False) -> str:
        return self.render(self.bundles[name], self.ctx(name, target, force=force))

    def render(self, bundle: PromptBundle, ctx: Ctx) -> str:
        chunks: list[str] = []
        for part in bundle.prompt:
            if isinstance(part, str):
                chunks.append(part)
            else:
                chunks.append(part.render(ctx))
        return "\n".join(c.rstrip() for c in chunks if c.strip()) + "\n"

    def run(
        self,
        name: str,
        target: Target,
        *,
        force: bool = False,
        dry_run: bool = False,
    ) -> Result:
        bundle = self.bundles[name]
        ctx = self.ctx(name, target, force=force, dry_run=dry_run)
        prompt = self.render(bundle, ctx)
        output = bundle.output(ctx)

        if dry_run:
            self.print_preview(ctx, prompt)
            return Result(name, target.label, Flow.cont(), output)

        for action in bundle.pre_exec:
            flow = action.run(ctx)
            if flow.state == "skip":
                return Result(name, target.label, flow, output)
            if flow.state == "error":
                raise click.ClickException(f"{name}: {flow.reason}")

        self.exec_agent(ctx, prompt)

        for action in bundle.post_exec:
            flow = action.run(ctx)
            if flow.state == "error":
                raise click.ClickException(f"{name}: {flow.reason}")

        return Result(name, target.label, Flow.cont(), output)

    def run_many(
        self,
        names: list[str],
        target: Target,
        *,
        force: bool = False,
        dry_run: bool = False,
        workers: int | None = None,
    ) -> list[Result]:
        if len(names) == 1 or dry_run:
            return [self.run(n, target, force=force, dry_run=dry_run) for n in names]

        max_workers = workers or min(len(names), 5)
        results: list[Result] = []
        with concurrent.futures.ThreadPoolExecutor(max_workers=max_workers) as pool:
            future_to_name = {
                pool.submit(self.run, n, target, force=force, dry_run=False): n
                for n in names
            }
            for future in concurrent.futures.as_completed(future_to_name):
                results.append(future.result())
        return results

    def exec_agent(self, ctx: Ctx, prompt: str) -> None:
        subprocess.run(
            [
                "ateam",
                "exec",
                "--work-dir",
                str(ctx.path),
                "--action",
                ctx.action,
                "--role",
                ctx.role,
            ],
            input=prompt,
            text=True,
            check=True,
        )

    def print_preview(self, ctx: Ctx, prompt: str) -> None:
        click.echo(
            f"---------- PROMPT (work-dir: {ctx.path}, "
            f"action: {ctx.action}, role: {ctx.role}) ----------"
        )
        click.echo(prompt)
        click.echo("---------- END PROMPT ----------")


# === Git state and manifest helpers ===

def git_state(project_dir: Path) -> tuple[str, str]:
    branch = subprocess.run(
        ["git", "symbolic-ref", "--short", "HEAD"],
        cwd=project_dir,
        capture_output=True,
        text=True,
    ).stdout.strip() or "DETACHED"
    commit = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=project_dir,
        capture_output=True,
        text=True,
    ).stdout.strip()
    return branch, commit


def read_files_toml(project: str) -> dict | None:
    p = files_toml_path(project)
    if not p.exists():
        return None
    return tomllib.loads(p.read_text())


def scope_files(t: Target, data: dict, scope: str) -> list[Path]:
    rels = data.get("files", {}).get(scope, []) or []
    return [t.path / r for r in rels]


def all_tracked_files(t: Target, data: dict) -> list[Path]:
    return [
        t.path / r
        for files in data.get("files", {}).values()
        for r in (files or [])
    ]


def files_newer_than(reference: Path, files: list[Path]) -> list[Path]:
    if not reference.exists():
        return [f for f in files if f.exists()]
    ref = reference.stat().st_mtime
    return [f for f in files if f.exists() and f.stat().st_mtime > ref]


def skip_followup(parent: Path, child: Path) -> bool:
    return child.exists() and parent.exists() and child.stat().st_mtime >= parent.stat().st_mtime


def read_or_placeholder(p: Path, missing: str) -> str:
    if not p.exists():
        return missing
    text = p.read_text()
    return text if text.strip() else missing


def changes_since_overview(ctx: Ctx) -> str:
    if ctx.scope is None:
        return "(no scope)"
    data = read_files_toml(ctx.label)
    if data is None:
        return "(no files.toml - discover has not run)"
    tracked = scope_files(ctx.target, data, ctx.scope)
    if not tracked:
        return f"(no files tracked for scope {ctx.scope!r})"
    op = overview_path(ctx.label)
    changed = files_newer_than(op, tracked) if op.exists() else tracked
    if not changed:
        return "(nothing has changed since the overview was written)"
    return "\n".join(f"- `{p.relative_to(ctx.path)}`" for p in changed)


# === Prompt text helpers ===

def intro(ctx: Ctx) -> str:
    suffix = f" (scope {ctx.scope!r})" if ctx.scope else ""
    return (
        f"You are running {ctx.action!r}{suffix} for project "
        f"{ctx.label!r} at {str(ctx.path)!r}.\n"
        "Keep your focus very narrow to the instruction below."
    )


def footer(ctx: Ctx, output: Path) -> str:
    return f"\nSave your report on project {ctx.label!r} ({str(ctx.path)!r}) at: {output}\n"


def discover_prompt(ctx: Ctx) -> str:
    data = read_files_toml(ctx.label)
    changed: list[Path] | None = None
    if data is not None:
        changed = files_newer_than(files_toml_path(ctx.label), all_tracked_files(ctx.target, data))

    branch, commit = git_state(ctx.path)
    now = datetime.now().strftime("%Y-%m-%dT%H:%M:%S")
    op = overview_path(ctx.label)
    fp = files_toml_path(ctx.label)
    meta = f"""\
[meta]
git_branch       = "{branch}"
git_commit_hash  = "{commit}"
discovered_at    = "{now}"
path_used        = "{ctx.target.path_abs}"
path_canonical   = "{ctx.target.path_canonical}"
"""

    if changed is None:
        body = f"""\
{intro(ctx)}

Goals - produce two files:
1. Overview: '{op}'
2. TOML manifest: '{fp}'

The overview is a concise narrative covering:
* what the project is, language/stack
* how to build, or "no build step"
* how to run tests by category, or "no tests"
* dev-environment setup, Docker, devcontainer, or similar if any
* anything non-obvious for troubleshooting

The TOML must use exactly this structure:

{meta}
[files]
claude    = ["CLAUDE.md"]            # use [] if absent
gitconfig = [".git/config"]
build     = ["Makefile", "..."]
test      = ["..."]
docker    = ["Dockerfile", "..."]

Paths are relative to '{ctx.path}'. List the smallest set of files an auditor
would need to read to evaluate each scope. Use [] for scopes with nothing
relevant.

Copy the [meta] block verbatim. Do not invent additional sections."""
    else:
        changed_list = "\n".join(f"  - {p.relative_to(ctx.path)}" for p in changed)
        body = f"""\
{intro(ctx)}

Incremental update. Existing artifacts:
* Overview: '{op}'
* Manifest: '{fp}'

Files changed since last discover:
{changed_list}

Inspect ONLY these files. Minimum-effort update:
* Update '{op}' only if the changes meaningfully affect what it says.
* Update '{fp}' [files] sections only if the relevant file lists changed.
* In both cases, even if you change nothing else, replace the [meta] block with:

{meta}"""

    return body + footer(ctx, op)


def audit_header(ctx: Ctx) -> str:
    return f"{intro(ctx)}\nUse the Project Overview above for context. Do NOT redo discovery."


def fix_prompt(ctx: Ctx) -> str:
    assert ctx.scope is not None
    audit_out = report_path("audit", ctx.scope, ctx.label)
    out = report_path("fix", ctx.scope, ctx.label)
    return f"""\
{intro(ctx)}
Look at '{audit_out}' if it exists; if not, you are done and your
last message must be exactly: "no audit items to implement".
Otherwise make the recommended changes inside '{ctx.path}'.
{footer(ctx, out)}"""


def verify_prompt(ctx: Ctx) -> str:
    assert ctx.scope is not None
    fix_out = report_path("fix", ctx.scope, ctx.label)
    out = report_path("verify", ctx.scope, ctx.label)
    return f"""\
{intro(ctx)}
Look at '{fix_out}' - it documents improvements for scope '{ctx.scope}'.
Go to '{ctx.path}' and try to follow the steps / run the commands.
If there are issues, fix them and update '{fix_out}' to be accurate.
For any change you make in that file, note that you made changes, why,
and what they are.
{footer(ctx, out)}"""


# === Scope-specific audit bodies ===

SCOPE_AUDIT: dict[str, Callable[[Ctx], str]] = {}


def scope_audit(name: str):
    def deco(fn: Callable[[Ctx], str]) -> Callable[[Ctx], str]:
        SCOPE_AUDIT[name] = fn
        return fn
    return deco


@scope_audit("claude")
def audit_claude(ctx: Ctx) -> str:
    return f"""\
Recommend improvements to CLAUDE.md in '{ctx.path}' so it clearly documents:
* git usage, if non-standard
* how to build, or N/A
* how to run tests, by category if multiple
* common troubleshooting steps
Be specific: cite missing sections, vague instructions, or commands that
do not match what the overview describes."""


@scope_audit("gitconfig")
def audit_gitconfig(ctx: Ctx) -> str:
    return f"""\
Verify git identity for '{ctx.label}' at '{ctx.path}':
* user.name is set, locally or globally - note which
* user.email is set, locally or globally - note which
Report what is configured. Any identity is acceptable for now."""


@scope_audit("build")
def audit_build(ctx: Ctx) -> str:
    return f"""\
Audit the build story for '{ctx.label}'. Flag:
* commands that do not work as documented
* missing prerequisites or env vars
* inconsistencies between overview and actual project files
If the stack does not need a build step, state that and stop."""


@scope_audit("test")
def audit_test(ctx: Ctx) -> str:
    return f"""\
Audit the test commands for '{ctx.label}', your work as 2 parts:

### Commands that exist today

Here we are only interested in what can be run today. Document:
* The test framework(s) in use
* The command for each category, and the directory it should be run from
* Any setup required to run tests, services, fixtures, env vars
If no tests exist, report that explicitly.

Write a structured toml file in '{ctx.output("tests.toml")}':

    [tests]
    fast=QUICK_VERIFICATION_COMMAND
    all=RUN_ABSOLUTELY_ALL_TESTS
    AREA_X=COMMAND_X
    AREA_Y=COMMAND_Y
    ...

where all caps words are replaced by actual commands. Areas are things like
'backend', 'frontend', 'cli', or 'benchmark'.

### How complete is the test story

Flag:
* test commands that do not match what the project actually supports
* missing categories, e.g. there are e2e tests but no documented runner
* broken or skipped suites worth surfacing
"""


@scope_audit("docker")
def audit_docker(ctx: Ctx) -> str:
    return f"""\
Audit the dev-environment-in-Docker setup for '{ctx.label}'. Flag:
* broken Dockerfile / compose / devcontainer
* commands that do not match what is documented
If no Docker setup exists, recommend whether one would be valuable given
the project's stack, and stop if not."""


def audit_prompt(ctx: Ctx) -> str:
    assert ctx.scope is not None
    out = report_path("audit", ctx.scope, ctx.label)
    return f"""\
# Context

# Project Overview

{read_or_placeholder(overview_path(ctx.label), "(no overview yet - discover has not run)")}

# File changes since overview was produced

{changes_since_overview(ctx)}

# Previous report

{read_or_placeholder(out, "(none)")}

# Action

{audit_header(ctx)}

{SCOPE_AUDIT[ctx.scope](ctx)}
{footer(ctx, out)}"""


# === ExecAction implementations for metaproject ===

def discover_is_fresh(ctx: Ctx) -> bool:
    if ctx.force:
        return False
    data = read_files_toml(ctx.label)
    if data is None:
        return False
    return not files_newer_than(files_toml_path(ctx.label), all_tracked_files(ctx.target, data))


def audit_is_fresh(ctx: Ctx) -> bool:
    assert ctx.scope is not None
    if ctx.force:
        return False
    out = report_path("audit", ctx.scope, ctx.label)
    data = read_files_toml(ctx.label)
    if data is None or not out.exists():
        return False
    files = scope_files(ctx.target, data, ctx.scope)
    if not files:
        return True
    return not files_newer_than(out, files)


def followup_is_fresh(parent_action: str, child_action: str) -> Callable[[Ctx], bool]:
    def check(ctx: Ctx) -> bool:
        assert ctx.scope is not None
        if ctx.force:
            return False
        parent = report_path(parent_action, ctx.scope, ctx.label)
        child = report_path(child_action, ctx.scope, ctx.label)
        return skip_followup(parent, child)

    return check


def skip_reason(ctx: Ctx) -> str:
    if ctx.scope:
        return f"{ctx.action}/{ctx.scope}/{ctx.label}: up to date"
    return f"{ctx.action}/{ctx.label}: up to date"


def output_paths_for(ctx: Ctx) -> list[Path]:
    if ctx.action == "discover":
        return [overview_path(ctx.label), files_toml_path(ctx.label)]
    assert ctx.scope is not None
    paths = [report_path(ctx.action, ctx.scope, ctx.label)]
    if ctx.action == "audit" and ctx.scope == "test":
        paths.append(structured_report_path("audit", "test", ctx.label, "tests.toml"))
    return paths


# === Bundle registration ===

def build_runner() -> Runner:
    runner = Runner()

    runner.add(
        PromptBundle(
            name="discover",
            prompt=[ExecPromptFn(discover_prompt)],
            pre_exec=[
                SkipIf(discover_is_fresh, skip_reason),
                EnsureParents(output_paths_for),
                BackupFiles(output_paths_for),
            ],
            output=lambda ctx: overview_path(ctx.label),
        )
    )

    for scope in ALL_SCOPES:
        runner.add(
            PromptBundle(
                name=f"audit/{scope}",
                prompt=[ExecPromptFn(audit_prompt)],
                pre_exec=[
                    SkipIf(audit_is_fresh, skip_reason),
                    EnsureParents(output_paths_for),
                    BackupFiles(output_paths_for),
                ],
                output=lambda ctx: report_path("audit", ctx.scope or "", ctx.label),
            )
        )

        runner.add(
            PromptBundle(
                name=f"fix/{scope}",
                prompt=[ExecPromptFn(fix_prompt)],
                pre_exec=[
                    SkipIf(followup_is_fresh("audit", "fix"), skip_reason),
                    EnsureParents(output_paths_for),
                    BackupFiles(output_paths_for),
                ],
                output=lambda ctx: report_path("fix", ctx.scope or "", ctx.label),
            )
        )

        runner.add(
            PromptBundle(
                name=f"verify/{scope}",
                prompt=[ExecPromptFn(verify_prompt)],
                pre_exec=[
                    SkipIf(followup_is_fresh("fix", "verify"), skip_reason),
                    EnsureParents(output_paths_for),
                    BackupFiles(output_paths_for),
                ],
                output=lambda ctx: report_path("verify", ctx.scope or "", ctx.label),
            )
        )

    return runner


# === Environment setup and meta actions ===

def init(verbose: bool) -> None:
    if not (BASE_DIR / ".ateam").is_dir():
        result = subprocess.run(
            ["ateam", "init"],
            cwd=BASE_DIR,
            capture_output=True,
            text=True,
        )
        if result.stderr:
            click.echo(result.stderr, err=True, nl=False)
        if result.returncode != 0:
            if result.stdout:
                click.echo(result.stdout, nl=False)
            raise click.ClickException(f"ateam init failed (exit {result.returncode})")

    env = subprocess.run(
        ["ateam", "--project", str(BASE_DIR), "env"],
        capture_output=True,
        text=True,
    )
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
    candidates = sorted(
        c for c in BASE_REPORTS.iterdir()
        if c.is_dir() and (c / "files.toml").exists()
    )
    if short:
        for c in candidates:
            click.echo(c.name)
        return

    rows: list[tuple[str, str, str, str, str]] = []
    for c in candidates:
        try:
            data = tomllib.loads((c / "files.toml").read_text())
        except tomllib.TOMLDecodeError as e:
            rows.append((c.name, f"(malformed: {e})", "", "", ""))
            continue
        meta = data.get("meta") or {}
        path = meta.get("path_canonical") or meta.get("path_used") or "?"
        branch = meta.get("git_branch", "?")
        commit = (meta.get("git_commit_hash") or "")[:8] or "?"
        ts = meta.get("discovered_at", "?")
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
    actions = {f"{a}.md" for a in ("audit", "fix", "verify")}
    for project_dir in sorted(BASE_REPORTS.iterdir()):
        if not project_dir.is_dir():
            continue
        for scope_dir in sorted(project_dir.iterdir()):
            if not scope_dir.is_dir():
                continue
            for f in sorted(scope_dir.iterdir()):
                if not f.is_file() or f.name not in actions:
                    continue
                rel = f.relative_to(BASE_DIR)
                click.echo(f"{project_dir.name},{scope_dir.name},{f.stem},{rel}")


# === Pipeline execution ===

def bundle_names_for_stage(stage: str, scopes: list[str]) -> list[str]:
    if stage == "discover":
        return ["discover"]
    return [f"{stage}/{scope}" for scope in scopes]


def run_target_pipeline(
    runner: Runner,
    target: Target,
    stages: list[str],
    scopes: list[str],
    *,
    force: bool,
    dry_run: bool,
) -> None:
    # Discover is a prerequisite for overview.md and files.toml. Even when the
    # requested action is audit/fix/verify, run discover first for this target.
    if any(stage in stages for stage in ("audit", "fix", "verify")) and "discover" not in stages:
        stages = ["discover"] + stages

    for stage in stages:
        names = bundle_names_for_stage(stage, scopes)
        results = runner.run_many(names, target, force=force, dry_run=dry_run)
        for result in sorted(results, key=lambda r: r.bundle):
            if result.flow.state == "skip":
                click.echo(f"= {result.bundle}/{result.target}: {result.flow.reason}")
            else:
                click.echo(f"-> {result.bundle}/{result.target}: {result.output or ''}")


# === CLI ===

@click.command(context_settings={"help_option_names": ["-h", "--help"]})
@click.argument("action", type=click.Choice(ALL_CLI_ACTIONS))
@click.argument("targets", nargs=-1, metavar="[TARGET...]")
@click.option("--dry-run", is_flag=True, help="Print prompts without executing.")
@click.option(
    "--scope",
    default="all",
    show_default=True,
    type=click.Choice(["all", *ALL_SCOPES]),
    help="Audit scope, ignored by discover.",
)
@click.option("--force", is_flag=True, help="Bypass all change-detection skips.")
@click.option("--short", is_flag=True, help="With projects: one name per line.")
@click.option(
    "--for",
    "for_action",
    type=click.Choice(PIPELINE),
    default="audit",
    show_default=True,
    help="With prompt: which pipeline step to preview.",
)
@click.option("-v", "--verbose", is_flag=True, help="Show ateam env output.")
def main(
    action: str,
    targets: tuple[str, ...],
    dry_run: bool,
    scope: str,
    force: bool,
    short: bool,
    for_action: str,
    verbose: bool,
) -> None:
    """Run discover/audit/fix/verify across projects.

    TARGET format: LABEL:PATH, for example myproj:/path/to/myproj.
    After a project has been discovered once, LABEL: is enough.

    Scoped stages run in parallel per target. Discover always runs first when a
    later stage needs overview.md and files.toml.
    """
    runner = build_runner()

    if action == "projects":
        if targets:
            raise click.UsageError("'projects' takes no targets")
        list_projects(short)
        return

    if action == "reports":
        if targets:
            raise click.UsageError("'reports' takes no targets")
        list_reports()
        return

    if action == "prompt":
        if len(targets) != 1:
            raise click.UsageError("'prompt' requires exactly one TARGET")
        target = Target.parse(targets[0])
        if for_action == "discover":
            click.echo(runner.preview("discover", target, force=force))
            return
        if scope == "all":
            raise click.UsageError(f"'prompt --for {for_action}' requires --scope")
        click.echo(runner.preview(f"{for_action}/{scope}", target, force=force))
        return

    if not targets:
        raise click.UsageError("at least one TARGET is required for this action")

    parsed = [Target.parse(t) for t in targets]
    stages = list(ACTION_CHAIN[action])
    scopes = ALL_SCOPES if scope == "all" else [scope]

    if not dry_run:
        init(verbose)

    for target in parsed:
        run_target_pipeline(
            runner,
            target,
            stages,
            scopes,
            force=force,
            dry_run=dry_run,
        )


if __name__ == "__main__":
    main()
```

## Why This Is Better Than The Current Script

The current script already has the right ingredients, but they are not first
class. This version makes them first class:

| Current script | Python API version |
|---|---|
| `build_audit_prompt()` manually assembles every section | `PromptBundle(prompt=[...])` names the prompt parts |
| `run_audit()` mixes skip, backup, prompt, and execution | `pre_exec`, `prompt`, and `Runner.run()` separate them |
| scope functions are registered separately from run behavior | each scope becomes `audit/<scope>`, `fix/<scope>`, `verify/<scope>` bundles |
| scoped work is serial | scoped bundles run through `Runner.run_many()` in parallel |
| preview is separate ad hoc code | `Runner.preview()` renders the same bundle prompt without actions |

The strongest improvement is the execution order:

```text
for each target:
  discover
  audit/claude, audit/gitconfig, audit/build, audit/test, audit/docker in parallel
  fix/claude, fix/gitconfig, fix/build, fix/test, fix/docker in parallel
  verify/claude, verify/gitconfig, verify/build, verify/test, verify/docker in parallel
```

That keeps overview generation deterministic and first, but removes the slow
serial loop over independent scopes.

## Remaining Design Questions

1. `PromptBundle` is a good name for one executable agent unit, but `Runner`
   might be too generic. `PromptRunner` or `AgentRunner` would be clearer in a
   larger codebase.
2. `ExecPromptFn` and `ExecActionFn` are adapter names, not user-facing
   concepts. The important names are still `ExecPrompt` and `ExecAction`.
3. The generic API should probably standardize result capture from `ateam exec`
   instead of assuming the agent writes directly to the instructed output path.
4. Parallel execution needs a configurable worker limit. Five scopes maps well
   to this example, but generic workflows need budget and concurrency controls.
5. If this became part of ateam, the Python API should expose the same preview
   and process metadata that the CLI exposes, not hide it behind subprocesses.
