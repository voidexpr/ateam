"""ateam.py — generic prompt-bundle runner on top of `ateam exec`.

Design artifact, not in-tree code. Mirrors `plans/prompt_example_metaproject_python_api.md`,
extended to support external prompt files with lightweight templating (the
"keep prompts outside the app" goal in
plans/Feature_prompt_report_fs_refactor.md).

A small Python framework for bundling a prompt with optional pre/post exec
actions. Bundles can be previewed (renders the prompt, no actions) or run
(executes the agent via `ateam exec`, with pre/post actions firing around the
agent call). Multiple bundles run in parallel via `run_many`.

Boundary with ateam itself:
  - Prompt assembly, templating, hooks, skip/error logic: Python (here).
  - Agent invocation, audit trail (call DB, cost, tokens), container, sandbox:
    ateam (`ateam exec`).
  - Per-job parallelism: Python (`ThreadPoolExecutor`); each thread is one
    `ateam exec` subprocess.

External prompt files:
  - `Runner(prompt_dir=...)` sets the base directory.
  - `PromptFile("relative/path.md")` is a `PromptPart` that reads + templates
    the file at render time.
  - Files may include other files via `{{include path}}` / `{{include? path}}`.

Templating syntax (substituted at render time):
  {{prompt.name}}      last path segment of the bundle name
                       (e.g. "test" for "audit/test")
  {{prompt.path}}      full bundle name
  {{arg.KEY}}          value from Ctx.args[KEY] (missing key → error)
  {{env.KEY}}          value from os.environ[KEY] (missing → error)
  {{exec.shared_dir}}  Runner(shared_dir=...)
  {{exec.runtime_dir}} Runner(runtime_dir=...)
  {{exec.work_dir}}    Ctx.work_dir
  {{include PATH}}     inline a file from prompt_dir; recursively expanded
  {{include? PATH}}    like include, but empty string if missing

Variables within a known namespace (prompt./arg./env./exec.) raise on missing
key — typos surface immediately. Unknown namespaces pass through unchanged so
authors can write literal `{{...}}` in prompts.

Open extension points (see Feature_prompt_report_fs_refactor.md "Forward look"):
  - Pre-exec `Flow("completed")` — script did the work, no LLM needed.
  - Post-exec `Flow("redo", extra=...)` — re-run with appended instruction.
  - Post-exec `Flow("fallback", profile=...)` / `Flow("retry", after=...)`.
  Not implemented here; the slots in `Runner.run` are marked with TODO.
"""
from __future__ import annotations

import concurrent.futures
import os
import re
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
    args: dict[str, str] = field(default_factory=dict)
    data: dict[str, Any] = field(default_factory=dict)


# === Protocols ===

class ExecPrompt(Protocol):
    """Produces text inserted into the prompt. Runs during preview."""
    def render(self, ctx: Ctx) -> str: ...


class ExecAction(Protocol):
    """Runs before or after the agent. Output is NOT in the prompt."""
    def run(self, ctx: Ctx) -> Flow: ...


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


# === External prompt files ===

@dataclass(frozen=True)
class PromptFile:
    """A prompt fragment read from Runner.prompt_dir, with templating applied."""
    path: str


PromptPart = str | ExecPrompt | PromptFile


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


# === Templating ===

_KNOWN_NAMESPACES = ("prompt.", "arg.", "env.", "exec.")
_VAR_RE = re.compile(r"\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}")
_INCLUDE_RE = re.compile(r"\{\{\s*(include\??)\s+([^\s}]+)\s*\}\}")
_MAX_INCLUDE_DEPTH = 16


class TemplateError(RuntimeError):
    pass


# === Runner ===

class Runner:
    def __init__(
        self,
        *,
        prompt_dir: Path | None = None,
        shared_dir: Path | None = None,
        runtime_dir: Path | None = None,
    ) -> None:
        self.bundles: dict[str, PromptBundle] = {}
        self.prompt_dir = prompt_dir
        self.shared_dir = shared_dir
        self.runtime_dir = runtime_dir

    def add(self, bundle: PromptBundle) -> None:
        if bundle.name in self.bundles:
            raise ValueError(f"duplicate bundle: {bundle.name}")
        self.bundles[bundle.name] = bundle

    def preview(self, name: str, ctx: Ctx) -> str:
        return self._render_prompt(self.bundles[name], ctx)

    def run(self, name: str, ctx: Ctx) -> Result:
        bundle = self.bundles[name]
        prompt = self._render_prompt(bundle, ctx)

        if ctx.dry_run:
            print(f"---- PROMPT ({name}, work-dir: {ctx.work_dir}) ----")
            print(prompt)
            print("---- END PROMPT ----")
            return Result(name, Flow())

        for action in bundle.pre_exec:
            flow = action.run(ctx)
            # TODO: Flow("completed") would also return here, recorded as
            # success rather than skip (the script wrote the output itself).
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
            # TODO: Flow("redo") / Flow("fallback") / Flow("retry") would loop
            # back to (a recomputed) subprocess.run above, with a max-attempts
            # guard and parent_exec_id correlation if/when ateam emits it.
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

    # --- assembly ---

    def _render_prompt(self, bundle: PromptBundle, ctx: Ctx) -> str:
        chunks = [self._resolve_part(p, ctx, bundle) for p in bundle.prompt]
        return "\n".join(c.rstrip() for c in chunks if c.strip()) + "\n"

    def _resolve_part(self, part: PromptPart, ctx: Ctx, bundle: PromptBundle) -> str:
        if isinstance(part, str):
            text = part
        elif isinstance(part, PromptFile):
            text = self._read_file(part.path)
        else:
            text = part.render(ctx)
        return self._expand(text, ctx, bundle)

    def _read_file(self, rel_path: str) -> str:
        if self.prompt_dir is None:
            raise TemplateError(
                f"prompt file {rel_path!r} referenced but Runner(prompt_dir=...) not set"
            )
        full = self.prompt_dir / rel_path
        try:
            return full.read_text()
        except FileNotFoundError:
            raise TemplateError(f"prompt file not found: {full}") from None

    def _expand(self, content: str, ctx: Ctx, bundle: PromptBundle,
                depth: int = 0) -> str:
        if depth > _MAX_INCLUDE_DEPTH:
            raise TemplateError(f"include depth exceeded ({_MAX_INCLUDE_DEPTH})")

        def _resolve_include(m: re.Match[str]) -> str:
            directive, raw_path = m.group(1), m.group(2)
            path = self._substitute_vars(raw_path, ctx, bundle)
            optional = directive == "include?"
            if optional and self.prompt_dir is not None \
                    and not (self.prompt_dir / path).exists():
                return ""
            nested = self._read_file(path)
            return self._expand(nested, ctx, bundle, depth + 1)

        content = _INCLUDE_RE.sub(_resolve_include, content)
        return self._substitute_vars(content, ctx, bundle)

    def _substitute_vars(self, content: str, ctx: Ctx, bundle: PromptBundle) -> str:
        def _resolve(m: re.Match[str]) -> str:
            key = m.group(1)
            if not any(key.startswith(ns) for ns in _KNOWN_NAMESPACES):
                return m.group(0)  # pass-through: literal {{...}} in prompt
            return self._lookup(key, ctx, bundle)
        return _VAR_RE.sub(_resolve, content)

    def _lookup(self, key: str, ctx: Ctx, bundle: PromptBundle) -> str:
        if key == "prompt.path":
            return bundle.name
        if key == "prompt.name":
            return bundle.name.rsplit("/", 1)[-1]
        if key == "exec.shared_dir":
            return str(self.shared_dir) if self.shared_dir else ""
        if key == "exec.runtime_dir":
            return str(self.runtime_dir) if self.runtime_dir else ""
        if key == "exec.work_dir":
            return str(ctx.work_dir)
        if key.startswith("arg."):
            sub = key[4:]
            if sub not in ctx.args:
                raise TemplateError(f"unknown arg key: {sub!r}")
            return ctx.args[sub]
        if key.startswith("env."):
            sub = key[4:]
            if sub not in os.environ:
                raise TemplateError(f"env var not set: {sub!r}")
            return os.environ[sub]
        raise TemplateError(f"unknown template variable: {key!r}")
