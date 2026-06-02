#!/usr/bin/env python3
"""demo-parallel-runner — run N `ateam exec` subprocesses in parallel
with a live progress table modeled on `ateam parallel`'s native one.

Each subprocess runs as `ateam exec --format jsonl <prompt>`, which emits
an interleaved bundle+agent JSONL event stream on stdout. We read that
stream per-process and aggregate it into a single shared table that
refreshes ~5x/sec, matching the columns of cmd/pool_status.go:

    ID  LABEL  STATUS  EstTOKENS  TURNS  DETAILS

Demonstrates the external-orchestrator pattern: spawn N subprocesses,
multiplex their JSONL streams, render whatever UI you want. The same
fields the native `ateam parallel` consumes via Reporter.AgentEvent are
on the wire.

Usage:
    demo-parallel-runner.py [--parallel N] [--profile P] [--agent A]
                            [-f PROMPTS_FILE] [prompts...]
    cat prompts.txt | demo-parallel-runner.py

Examples:
    demo-parallel-runner.py -j 3 "list files" "count tests" "summarize README"
    demo-parallel-runner.py -j 4 -f prompts.txt --profile sandbox
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import threading
import time
from dataclasses import dataclass

# Column format mirrors cmd/pool_status.go::poolStatusRowFmt so the layout
# matches `ateam parallel`'s native table.
ROW_FMT = "  {:<7} {:<25} {:<8} {:<9} {:<6} {}"
HEADER = ROW_FMT.format("ID", "LABEL", "STATUS", "EstTOKENS", "TURNS", "DETAILS")

STATE_QUEUED = "queued"
STATE_RUNNING = "running"
STATE_DONE = "done"
STATE_ERROR = "ERROR"


@dataclass
class Row:
    label: str
    state: str = STATE_QUEUED
    exec_id: int = 0
    est_tokens: int = 0
    turns: int = 0
    tool_name: str = ""
    tool_count: int = 0
    started_at: float = 0.0
    ended_at: float = 0.0
    cost_usd: float = 0.0
    final_tokens: int = 0
    detail_extra: str = ""


def fmt_tokens(n):
    if n <= 0:
        return ""
    if n < 1000:
        return str(n)
    if n < 1_000_000:
        return f"{n / 1000:.1f}k"
    return f"{n / 1_000_000:.2f}M"


def fmt_elapsed(seconds):
    if seconds <= 0:
        return ""
    if seconds < 60:
        return f"{int(seconds)}s"
    m, s = divmod(int(seconds), 60)
    if m < 60:
        return f"{m}m{s:02d}s"
    h, m = divmod(m, 60)
    return f"{h}h{m:02d}m"


def truncate(s, n):
    s = s.replace("\n", " ").replace("\r", " ")
    if len(s) <= n:
        return s
    return s[: n - 1] + "…"


def format_row(row, now):
    exec_id = str(row.exec_id) if row.exec_id else ""
    label = truncate(row.label, 25)
    turns = str(row.turns) if row.turns else ""
    est_tokens = fmt_tokens(row.est_tokens)

    if row.state == STATE_QUEUED:
        details = ""
    elif row.state in (STATE_DONE, STATE_ERROR):
        elapsed = fmt_elapsed(row.ended_at - row.started_at) if row.started_at else ""
        cost = f"${row.cost_usd:.2f}" if row.cost_usd else "$0.00"
        tokens = fmt_tokens(row.final_tokens)
        parts = [elapsed, cost]
        if tokens:
            parts.append("tokens: " + tokens)
        if row.detail_extra:
            parts.append(row.detail_extra)
        details = "  ".join(p for p in parts if p)
    else:
        elapsed = fmt_elapsed(now - row.started_at) if row.started_at else ""
        if row.tool_name:
            unit = "tool call" if row.tool_count == 1 else "tool calls"
            details = f"{elapsed}  {row.tool_name} ({row.tool_count} {unit})".strip()
        else:
            details = elapsed
    return ROW_FMT.format(exec_id, label, row.state, est_tokens, turns, details)


class Tracker:
    """Shared mutable table state with one lock for the whole table."""

    def __init__(self, rows, out=sys.stdout):
        self.rows = rows
        self.lock = threading.Lock()
        self.out = out
        self.is_tty = hasattr(out, "isatty") and out.isatty()
        self.last_lines = 0

    def update(self, idx, fn):
        with self.lock:
            fn(self.rows[idx])

    def render(self):
        with self.lock:
            now = time.monotonic()
            lines = [HEADER] + [format_row(r, now) for r in self.rows]
            if self.is_tty:
                if self.last_lines:
                    # ANSI: cursor up N lines, then clear to end of screen.
                    self.out.write(f"\033[{self.last_lines}A\033[J")
                self.out.write("\n".join(lines) + "\n")
            else:
                # Non-TTY: append the current snapshot once.
                self.out.write("\n".join(lines) + "\n\n")
            self.out.flush()
            self.last_lines = len(lines)


def apply_event(row, event):
    """Mutate `row` in place from one decoded JSONL event."""
    src = event.get("source")
    if src == "bundle":
        kind = event.get("kind")
        if kind == "agent_exec_start":
            if not row.exec_id:
                row.exec_id = event.get("exec_id", 0) or 0
            if not row.started_at:
                row.started_at = time.monotonic()
            if row.state == STATE_QUEUED:
                row.state = STATE_RUNNING
        elif kind == "agent_exec_end":
            row.ended_at = time.monotonic()
            row.cost_usd = float(event.get("cost_usd") or 0.0)
            inp = event.get("input_tokens") or 0
            outp = event.get("output_tokens") or 0
            row.final_tokens = inp + outp
            if row.final_tokens > row.est_tokens:
                row.est_tokens = row.final_tokens
            row.state = STATE_ERROR if event.get("is_error") else STATE_DONE
    elif src == "agent":
        if not row.started_at:
            row.started_at = time.monotonic()
        if row.state == STATE_QUEUED:
            row.state = STATE_RUNNING
        tc = event.get("turn_count") or 0
        if tc > row.turns:
            row.turns = tc
        cum = (event.get("cum_input_tokens") or 0) + (event.get("cum_output_tokens") or 0)
        if cum > row.est_tokens:
            row.est_tokens = cum
        tn = event.get("tool_name")
        if tn:
            row.tool_name = tn
        toolc = event.get("tool_count") or 0
        if toolc > row.tool_count:
            row.tool_count = toolc
        phase = event.get("phase")
        if phase == "done" and row.state == STATE_RUNNING:
            row.state = STATE_DONE
        elif phase == "error":
            row.state = STATE_ERROR


def reader_loop(idx, stream, tracker):
    for raw in stream:
        line = raw.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        tracker.update(idx, lambda r, e=event: apply_event(r, e))


def renderer_loop(tracker, stop):
    while not stop.is_set():
        tracker.render()
        if stop.wait(0.2):
            break
    tracker.render()


def run_task(idx, prompt, tracker, base_args, sem):
    with sem:
        cmd = ["ateam", "exec", "--format", "jsonl", *base_args, "--", prompt]
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            bufsize=1,
        )
        try:
            reader_loop(idx, proc.stdout, tracker)
        finally:
            rc = proc.wait()
        if rc != 0:
            def mark_err(r, rc=rc):
                if r.state not in (STATE_DONE, STATE_ERROR):
                    r.state = STATE_ERROR
                if not r.ended_at:
                    r.ended_at = time.monotonic()
                if not r.detail_extra:
                    r.detail_extra = f"exit {rc}"
            tracker.update(idx, mark_err)
        return rc


def read_prompts(args):
    prompts = []
    if args.prompts_file:
        with open(args.prompts_file) as f:
            for line in f:
                line = line.strip()
                if line and not line.startswith("#"):
                    prompts.append(line)
    prompts.extend(args.prompts)
    if not prompts and not sys.stdin.isatty():
        for line in sys.stdin:
            line = line.strip()
            if line and not line.startswith("#"):
                prompts.append(line)
    return prompts


def main():
    p = argparse.ArgumentParser(
        description="Run N ateam exec subprocesses in parallel with a live "
                    "progress table modeled on `ateam parallel`.",
    )
    p.add_argument("-j", "--parallel", type=int, default=4,
                   help="max concurrent ateam exec subprocesses (default 4)")
    p.add_argument("-f", "--prompts-file",
                   help="file with one prompt per line; # comments and blanks skipped")
    p.add_argument("--profile", help="ateam --profile to forward")
    p.add_argument("--agent", help="ateam --agent to forward")
    p.add_argument("prompts", nargs="*",
                   help="prompts as positional args (combined with --prompts-file / stdin)")
    args = p.parse_args()

    if not shutil.which("ateam"):
        print("error: 'ateam' not on PATH", file=sys.stderr)
        return 127

    prompts = read_prompts(args)
    if not prompts:
        print("error: no prompts given (use args, -f, or pipe via stdin)", file=sys.stderr)
        return 2

    base_args = []
    if args.profile:
        base_args += ["--profile", args.profile]
    if args.agent:
        base_args += ["--agent", args.agent]

    rows = [Row(label=prompt) for prompt in prompts]
    tracker = Tracker(rows)
    sem = threading.Semaphore(max(1, args.parallel))

    stop = threading.Event()
    renderer = threading.Thread(target=renderer_loop, args=(tracker, stop), daemon=True)
    renderer.start()

    workers = []
    results = [None] * len(prompts)

    def run(i, prompt):
        results[i] = run_task(i, prompt, tracker, base_args, sem)

    for i, prompt in enumerate(prompts):
        t = threading.Thread(target=run, args=(i, prompt))
        t.start()
        workers.append(t)

    try:
        for t in workers:
            t.join()
    except KeyboardInterrupt:
        # Child ateam execs inherit SIGINT via the terminal process group;
        # let them drain rather than killing the renderer abruptly.
        for t in workers:
            t.join()

    stop.set()
    renderer.join()

    failed = sum(1 for rc in results if rc not in (0, None))
    succeeded = len(results) - failed
    print(f"\n{succeeded} succeeded, {failed} failed ({len(results)} total)")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
