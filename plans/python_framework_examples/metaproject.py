#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["click>=8.1"]
# ///
"""metaproject.py — discover/audit/fix/verify across projects.

Design artifact, not in-tree. Companion to plans/ateam.py.

Runner configuration:
  - prompt_dir: plans/prompts/metaproject/   — external audit-body prompts.
  - shared_dir: plans/reports/               — cross-agent artifact tree.

The scope-specific audit bodies (claude/gitconfig/build/test/docker) live as
external .md files under prompt_dir. Discovery / audit context / fix / verify
keep Python control flow because they branch on filesystem state.
"""
from __future__ import annotations

import subprocess
import tomllib
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Callable

import click

from ateam import (
    Ctx, EnsureParents, BackupFiles, PromptBundle, PromptFile, PromptFn,
    Runner, SkipIf,
)

SCRIPT_DIR = Path(__file__).resolve().parent
PROMPT_DIR = SCRIPT_DIR / "prompts" / "metaproject"
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
        args={"label": target.label, "scope": scope, "action": action},
        data={"target": target},
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
    action, scope, label = ctx.args["action"], ctx.args["scope"], ctx.args["label"]
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
    label = ctx.args["label"]
    data = read_files_toml(label)
    if data is None:
        return False
    return not files_newer_than(files_toml_path(label),
                                all_tracked_files(ctx.data["target"], data))

def audit_is_fresh(ctx: Ctx) -> bool:
    if ctx.force:
        return False
    label, scope = ctx.args["label"], ctx.args["scope"]
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
        label, scope = ctx.args["label"], ctx.args["scope"]
        p, c = report_path(parent, scope, label), report_path(child, scope, label)
        return c.exists() and p.exists() and c.stat().st_mtime >= p.stat().st_mtime
    return check

def skip_reason(ctx: Ctx) -> str:
    label, scope, action = ctx.args["label"], ctx.args["scope"], ctx.args["action"]
    return f"{action}/{scope}/{label}: up to date" if scope else f"{action}/{label}: up to date"


# === Prompt builders ===

def intro(ctx: Ctx) -> str:
    scope = ctx.args["scope"]
    sx = f" (scope {scope!r})" if scope else ""
    target = ctx.data["target"]
    return (f"You are running {ctx.args['action']!r}{sx} for project "
            f"{ctx.args['label']!r} at '{target.path}'.\n"
            "Keep your focus very narrow to the instruction below.")

def footer(ctx: Ctx, out: Path) -> str:
    return f"\nSave your report on project '{ctx.args['label']}' at: {out}\n"


def discover_prompt(ctx: Ctx) -> str:
    label = ctx.args["label"]
    target = ctx.data["target"]
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


def audit_context(ctx: Ctx) -> str:
    """Pre-body context: overview, file changes, previous report, action header.

    The scope-specific body comes from prompts/metaproject/audit/<scope>.md
    via PromptFile in the bundle.
    """
    label, scope = ctx.args["label"], ctx.args["scope"]
    target = ctx.data["target"]
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
"""


def audit_footer(ctx: Ctx) -> str:
    out = report_path("audit", ctx.args["scope"], ctx.args["label"])
    return footer(ctx, out)


def fix_prompt(ctx: Ctx) -> str:
    label, scope = ctx.args["label"], ctx.args["scope"]
    audit_out = report_path("audit", scope, label)
    out = report_path("fix", scope, label)
    target = ctx.data["target"]
    return f"""{intro(ctx)}
Look at '{audit_out}' if it exists; if not, you are done and your last
message must be exactly: "no audit items to implement".
Otherwise make the recommended changes inside '{target.path}'.
{footer(ctx, out)}"""


def verify_prompt(ctx: Ctx) -> str:
    label, scope = ctx.args["label"], ctx.args["scope"]
    fix_out = report_path("fix", scope, label)
    out = report_path("verify", scope, label)
    target = ctx.data["target"]
    return f"""{intro(ctx)}
Look at '{fix_out}' — it documents improvements for scope '{scope}'.
Go to '{target.path}' and try to follow the steps / run the commands.
If there are issues, fix them and update '{fix_out}' to be accurate. For any
change in that file, note that you made changes, why, and what they are.
{footer(ctx, out)}"""


# === Bundle registration ===

def build_runner() -> Runner:
    r = Runner(prompt_dir=PROMPT_DIR, shared_dir=BASE_REPORTS)
    setup = [EnsureParents(outputs_for), BackupFiles(outputs_for)]
    r.add(PromptBundle(
        name="discover",
        prompt=[PromptFn(discover_prompt)],
        pre_exec=[SkipIf(discover_is_fresh, skip_reason), *setup],
    ))
    for scope in ALL_SCOPES:
        r.add(PromptBundle(
            f"audit/{scope}",
            prompt=[
                PromptFn(audit_context),
                PromptFile(f"audit/{scope}.md"),
                PromptFn(audit_footer),
            ],
            pre_exec=[SkipIf(audit_is_fresh, skip_reason), *setup],
        ))
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
