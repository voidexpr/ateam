#!/usr/bin/env python3
"""
stream-audit — analyze token consumption and tool overlap across ateam runs.

Reads .ateam/logs/<exec_id>/stream.jsonl files emitted by Claude-Code-driven
ateam runs and reports on token consumption, tool-call distribution, and
cross-role discovery overlap.

Commands:
    summary    per-run token/cost/turn-count table (one line per exec)
    tools      tool-call distribution per role
    overlap    files and canonical calls shared across roles
    warmup     orientation density in the first N tool calls
    discovery  discovery vs analysis token split (cache-amplified)
    baseline   static-baseline amplification share of cache_read
    all        run every subcommand above (default)

Inputs:
    [exec_ids ...]    positional args. Default: every exec id under --logs-dir.
    --logs-dir PATH   override .ateam/logs/ (default ./.ateam/logs)
    --top N           cap "top N" lists (default 20)
    --json            emit machine-readable JSON instead of tables
    --warmup-window N inspect the first N calls per role for `warmup` (default 10)

Notes:
    - "Discovery" tool calls are Glob / Grep / LS, plus Bash invocations whose
      first command is ls / find / grep / rg / wc / tree / fd / git.
    - "Orientation" is a tighter subset: the project-layout-discovery Bash
      patterns (ls /, ls .ateam/, ls .ateam/runtime/<id>/, find .ateam …) that
      tend to dominate the warmup turns.
    - Cache amplification approximates the cost of a tool result being re-read
      via the prompt cache on every subsequent turn of the run. A tool result
      emitted at turn k of an N-turn run is counted (N - k + 1) times.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from collections import Counter, defaultdict
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Iterable


# ---------- model & parsing ----------

DISCOVERY_BASH_HEADS = {
    "ls", "gls", "find", "gfind", "grep", "rg", "ggrep", "egrep", "fgrep",
    "wc", "gwc", "tree", "fd", "fdfind", "git",
}

# A tighter "warmup orientation" filter: project-layout discovery, not work.
ORIENTATION_BASH_HEADS = {"ls", "gls", "find", "gfind", "cat", "head", "tail", "mkdir"}


@dataclass
class ToolCall:
    id: str
    name: str
    input: dict
    # Filled in once tool_results are reconciled:
    result_size: int = 0
    turn_idx: int = 0  # 1-indexed turn at which the result arrived


@dataclass
class Run:
    exec_id: int
    role: str
    action: str  # report / review / etc.
    project_dir: str
    tool_calls: list[ToolCall] = field(default_factory=list)
    total_turns: int = 0
    result_event: dict | None = None
    baseline_tokens: int = 0  # first-turn cache_creation_input_tokens

    @property
    def usage(self) -> dict:
        return (self.result_event or {}).get("usage", {})

    @property
    def cost_usd(self) -> float:
        return (self.result_event or {}).get("total_cost_usd", 0.0) or 0.0


def role_from_prompt(prompt_md_path: Path) -> tuple[str, str]:
    """Extract (role, action) from the prompt.md header.

    Two header shapes are recognized:
        "<project> role <role-name> <action>"           (per-role report runs)
        "<project> the supervisor <action>"             (supervisor reviews)
    Falls back to ("?", "?") if not parseable.
    """
    try:
        with prompt_md_path.open() as f:
            first = f.readline().strip()
    except OSError:
        return "?", "?"
    m = re.search(r"\brole\s+(\S+)\s+(\S+)\s*$", first)
    if m:
        return m.group(1), m.group(2)
    m = re.search(r"\bthe supervisor\s+(\S+)\s*$", first)
    if m:
        return "supervisor", m.group(1)
    return "?", "?"


def load_run(exec_id: int, logs_dir: Path) -> Run | None:
    stream = logs_dir / str(exec_id) / "stream.jsonl"
    if not stream.exists():
        return None
    role, action = role_from_prompt(logs_dir / str(exec_id) / "prompt.md")
    run = Run(exec_id=exec_id, role=role, action=action, project_dir="")

    id_to_call: dict[str, ToolCall] = {}
    turn_idx = 0

    with stream.open() as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue

            t = e.get("type")
            if t == "system" and e.get("subtype") == "init":
                run.project_dir = e.get("cwd", "")
            elif t == "assistant":
                turn_idx += 1
                usage = e.get("message", {}).get("usage", {})
                if usage and run.baseline_tokens == 0:
                    run.baseline_tokens = usage.get("cache_creation_input_tokens", 0)
                for c in e.get("message", {}).get("content", []) or []:
                    if c.get("type") == "tool_use":
                        call = ToolCall(
                            id=c.get("id", ""),
                            name=c.get("name", ""),
                            input=c.get("input", {}) or {},
                        )
                        id_to_call[call.id] = call
                        run.tool_calls.append(call)
            elif t == "user":
                content = e.get("message", {}).get("content", [])
                if not isinstance(content, list):
                    continue
                for c in content:
                    if not isinstance(c, dict) or c.get("type") != "tool_result":
                        continue
                    tid = c.get("tool_use_id")
                    call = id_to_call.get(tid)
                    if not call:
                        continue
                    payload = c.get("content", "")
                    if isinstance(payload, list):
                        payload = " ".join(
                            p.get("text", "") if isinstance(p, dict) else str(p)
                            for p in payload
                        )
                    call.result_size = len(str(payload))
                    call.turn_idx = turn_idx
            elif t == "result":
                run.result_event = e

    run.total_turns = turn_idx
    return run


def discover_exec_ids(logs_dir: Path) -> list[int]:
    out: list[int] = []
    if not logs_dir.is_dir():
        return out
    for entry in sorted(logs_dir.iterdir()):
        if entry.name.isdigit() and (entry / "stream.jsonl").exists():
            out.append(int(entry.name))
    return sorted(out)


# ---------- classification ----------

def bash_head(cmd: str) -> str:
    """Return the first program a Bash command will run, after stripping any
    leading `cd … && …` prefix and following only the first pipe/&& segment."""
    cmd = cmd.strip()
    # strip leading `cd X && …`
    m = re.match(r"^(?:cd\s+\S+\s*(?:&&|;)\s*)?(.*)$", cmd, re.DOTALL)
    if m:
        cmd = m.group(1).strip()
    first = re.split(r"[|;&]", cmd, maxsplit=1)[0].strip()
    parts = first.split()
    return parts[0] if parts else ""


def is_discovery(call: ToolCall) -> bool:
    if call.name in ("Glob", "Grep", "LS"):
        return True
    if call.name == "Bash":
        return bash_head(call.input.get("command", "")) in DISCOVERY_BASH_HEADS
    return False


def is_orientation(call: ToolCall) -> bool:
    """The narrower 'warmup orientation' subset: layout-discovery only."""
    if call.name in ("Glob", "LS"):
        return True
    if call.name == "Bash":
        return bash_head(call.input.get("command", "")) in ORIENTATION_BASH_HEADS
    return False


def canonicalize(call: ToolCall, project_dir: str) -> tuple:
    """Normalize a tool call so identical calls across runs hash equal.

    - Absolute paths under project_dir are stripped.
    - Numeric exec ids in `.../runtime/<n>/` and `.../logs/<n>/` are replaced
      with `<id>` so per-run-id variation doesn't fragment the count.
    """
    prefix = project_dir.rstrip("/") + "/" if project_dir else ""

    def strip(s: str) -> str:
        if prefix:
            s = s.replace(prefix, "")
        s = re.sub(r"/runtime/\d+(?=/|\b)", "/runtime/<id>", s)
        s = re.sub(r"/logs/\d+(?=/|\b)", "/logs/<id>", s)
        return s

    if call.name == "Read":
        return ("Read", strip(call.input.get("file_path", "")))
    if call.name == "Bash":
        return ("Bash", strip(call.input.get("command", ""))[:400])
    if call.name == "Grep":
        return (
            "Grep",
            call.input.get("pattern", ""),
            strip(call.input.get("path", "")),
            call.input.get("glob", ""),
        )
    if call.name == "Glob":
        return ("Glob", call.input.get("pattern", ""))
    return (call.name, json.dumps(call.input, sort_keys=True)[:200])


# ---------- subcommands ----------

def cmd_summary(runs: list[Run], top: int, as_json: bool) -> None:
    rows = []
    totals = Counter()
    for r in runs:
        u = r.usage
        row = {
            "exec_id": r.exec_id,
            "role": r.role,
            "action": r.action,
            "turns": r.total_turns,
            "tool_calls": len(r.tool_calls),
            "cost_usd": r.cost_usd,
            "input": u.get("input_tokens", 0),
            "cache_read": u.get("cache_read_input_tokens", 0),
            "cache_write": u.get("cache_creation_input_tokens", 0),
            "output": u.get("output_tokens", 0),
        }
        rows.append(row)
        for k in ("cost_usd", "input", "cache_read", "cache_write", "output", "tool_calls"):
            totals[k] += row[k]

    if as_json:
        print(json.dumps({"rows": rows, "totals": dict(totals)}, indent=2))
        return

    print(
        f"{'ID':>3} {'ROLE':<28} {'ACT':<7} {'TURNS':>5} {'TOOLS':>5} {'COST':>7} "
        f"{'INPUT':>8} {'CACHE_R':>10} {'CACHE_W':>9} {'OUTPUT':>7}"
    )
    for row in rows:
        print(
            f"{row['exec_id']:>3} {row['role']:<28} {row['action']:<7} "
            f"{row['turns']:>5} {row['tool_calls']:>5} ${row['cost_usd']:>6.2f} "
            f"{row['input']:>8,} {row['cache_read']:>10,} {row['cache_write']:>9,} "
            f"{row['output']:>7,}"
        )
    print(
        f"\nTotals over {len(rows)} runs: ${totals['cost_usd']:.2f}, "
        f"{totals['tool_calls']} tool calls, cache_read={totals['cache_read']:,}, "
        f"cache_write={totals['cache_write']:,}, output={totals['output']:,}"
    )


def cmd_tools(runs: list[Run], top: int, as_json: bool) -> None:
    per_role: dict[str, Counter] = defaultdict(Counter)
    overall = Counter()
    bash_per_role: dict[str, Counter] = defaultdict(Counter)
    bash_overall = Counter()

    for r in runs:
        for c in r.tool_calls:
            per_role[r.role][c.name] += 1
            overall[c.name] += 1
            if c.name == "Bash":
                head = bash_head(c.input.get("command", ""))
                bash_per_role[r.role][head] += 1
                bash_overall[head] += 1

    if as_json:
        out = {
            "overall": dict(overall),
            "per_role": {r: dict(c) for r, c in per_role.items()},
            "bash_overall": dict(bash_overall),
            "bash_per_role": {r: dict(c) for r, c in bash_per_role.items()},
        }
        print(json.dumps(out, indent=2))
        return

    print("=== Tool distribution overall ===")
    for name, n in overall.most_common():
        print(f"  {name:<10} {n:>5}")

    print("\n=== Per-role tool counts ===")
    cols = ("Read", "Bash", "Grep", "Glob", "LS", "Write", "Task")
    header = f"{'ROLE':<28} {'TOTAL':>6} " + " ".join(f"{c:>6}" for c in cols)
    print(header)
    for role in sorted(per_role):
        c = per_role[role]
        print(
            f"{role:<28} {sum(c.values()):>6} "
            + " ".join(f"{c.get(col, 0):>6}" for col in cols)
        )

    print(f"\n=== Bash first-word distribution (top {top}) ===")
    for head, n in bash_overall.most_common(top):
        head_display = head if head else "(empty)"
        print(f"  {head_display:<20} {n:>5}")


def cmd_overlap(runs: list[Run], top: int, as_json: bool) -> None:
    # Group runs by project_dir so canonicalization strips the right prefix.
    canonical_calls: dict[tuple, list[dict]] = defaultdict(list)
    file_reads: dict[str, list[tuple[str, int]]] = defaultdict(list)

    for r in runs:
        for c in r.tool_calls:
            key = canonicalize(c, r.project_dir)
            canonical_calls[key].append(
                {"role": r.role, "exec_id": r.exec_id, "size": c.result_size}
            )
            if c.name == "Read":
                file_reads[key[1]].append((r.role, c.result_size))

    total_bytes = sum(c.result_size for r in runs for c in r.tool_calls)
    redundant_bytes = 0
    redundant_calls = 0
    for key, calls in canonical_calls.items():
        roles = {x["role"] for x in calls}
        if len(roles) >= 2:
            # first occurrence (by exec_id) is "free"; rest are redundant
            for c in sorted(calls, key=lambda x: x["exec_id"])[1:]:
                redundant_bytes += c["size"]
                redundant_calls += 1

    overlapping_files = sorted(
        (
            (path, info, len({r for r, _ in info}), sum(s for _, s in info))
            for path, info in file_reads.items()
            if len({r for r, _ in info}) >= 2
        ),
        key=lambda x: -x[3],
    )

    if as_json:
        out = {
            "total_tool_result_bytes": total_bytes,
            "redundant_bytes_identical_call": redundant_bytes,
            "redundant_call_count": redundant_calls,
            "redundant_ratio": redundant_bytes / max(1, total_bytes),
            "top_shared_files": [
                {
                    "path": p,
                    "distinct_roles": dr,
                    "read_count": len(info),
                    "total_bytes": tb,
                }
                for p, info, dr, tb in overlapping_files[:top]
            ],
        }
        print(json.dumps(out, indent=2))
        return

    pct = 100 * redundant_bytes / max(1, total_bytes)
    print(
        f"Tool-result bytes total: {total_bytes:,}  (~{total_bytes // 4:,} tokens)"
    )
    print(
        f"Redundant (identical canonical call across ≥2 roles): "
        f"{redundant_bytes:,} bytes ({pct:.1f}%) over {redundant_calls} calls"
    )

    print(f"\n=== Top {top} files Read by multiple roles ===")
    for path, info, distinct_roles, total_b in overlapping_files[:top]:
        print(
            f"  {distinct_roles:>2} roles, {len(info):>2} reads, "
            f"{total_b:>8,}b  {path}"
        )


def cmd_warmup(runs: list[Run], top: int, as_json: bool, window: int) -> None:
    densities = []
    pattern_counts: Counter = Counter()
    pattern_roles: dict[str, set] = defaultdict(set)

    for r in runs:
        window_calls = r.tool_calls[:window]
        orient = sum(1 for c in window_calls if is_orientation(c))
        densities.append(
            {
                "exec_id": r.exec_id,
                "role": r.role,
                "orientation_calls": orient,
                "window": window,
            }
        )
        for c in window_calls:
            if c.name != "Bash":
                continue
            cmd = c.input.get("command", "")
            norm = re.sub(r"/runtime/\d+/?", "/runtime/<id>/", cmd)
            if r.project_dir:
                norm = norm.replace(r.project_dir.rstrip("/") + "/", "")
            norm = re.sub(r"\s+", " ", norm).strip()
            if not norm:
                continue
            pattern_counts[norm[:200]] += 1
            pattern_roles[norm[:200]].add(r.role)

    if as_json:
        out = {
            "densities": densities,
            "top_patterns": [
                {"pattern": p, "calls": n, "distinct_roles": len(pattern_roles[p])}
                for p, n in pattern_counts.most_common(top)
            ],
        }
        print(json.dumps(out, indent=2))
        return

    print(f"=== Orientation density in first {window} calls per role ===")
    for d in densities:
        bar = "█" * d["orientation_calls"] + "·" * (window - d["orientation_calls"])
        print(
            f"  {d['role']:<28} (#{d['exec_id']:>2}): "
            f"{d['orientation_calls']:>2}/{window} {bar}"
        )

    print(f"\n=== Top {top} warmup Bash patterns ===")
    for pat, n in pattern_counts.most_common(top):
        roles_n = len(pattern_roles[pat])
        print(f"  {n:>3} calls / {roles_n:>2} roles | {pat[:130]}")


def cmd_discovery(runs: list[Run], top: int, as_json: bool) -> None:
    per_role = []
    totals = {"disc_raw": 0, "anal_raw": 0, "disc_amp": 0, "anal_amp": 0}

    for r in runs:
        disc_raw = anal_raw = disc_amp = anal_amp = 0
        for c in r.tool_calls:
            tokens = c.result_size // 4  # crude bytes→tokens
            remaining = max(0, r.total_turns - c.turn_idx)
            amplified = tokens * (1 + remaining)
            if is_discovery(c):
                disc_raw += tokens
                disc_amp += amplified
            else:
                anal_raw += tokens
                anal_amp += amplified

        amp_pct = 100 * disc_amp / max(1, disc_amp + anal_amp)
        per_role.append(
            {
                "exec_id": r.exec_id,
                "role": r.role,
                "turns": r.total_turns,
                "discovery_raw_tokens": disc_raw,
                "analysis_raw_tokens": anal_raw,
                "discovery_amplified_tokens": disc_amp,
                "analysis_amplified_tokens": anal_amp,
                "discovery_share_amplified_pct": amp_pct,
            }
        )
        totals["disc_raw"] += disc_raw
        totals["anal_raw"] += anal_raw
        totals["disc_amp"] += disc_amp
        totals["anal_amp"] += anal_amp

    if as_json:
        print(json.dumps({"per_role": per_role, "totals": totals}, indent=2))
        return

    print(
        f"{'ROLE':<28} {'TURNS':>5} {'DISC_RAW':>9} {'ANAL_RAW':>9} "
        f"{'DISC_AMP':>11} {'ANAL_AMP':>11} {'DISC%':>5}"
    )
    for row in per_role:
        print(
            f"{row['role']:<28} {row['turns']:>5} "
            f"{row['discovery_raw_tokens']:>9,} {row['analysis_raw_tokens']:>9,} "
            f"{row['discovery_amplified_tokens']:>11,} "
            f"{row['analysis_amplified_tokens']:>11,} "
            f"{row['discovery_share_amplified_pct']:>4.0f}%"
        )
    share = 100 * totals["disc_amp"] / max(1, totals["disc_amp"] + totals["anal_amp"])
    print(
        f"\nTotals: discovery_raw={totals['disc_raw']:,}  "
        f"analysis_raw={totals['anal_raw']:,}  "
        f"discovery_share_amplified={share:.1f}%"
    )


def cmd_baseline(runs: list[Run], top: int, as_json: bool) -> None:
    """Estimate the share of cache_read attributable to the static role baseline.

    The baseline (system prompt + tool registry + role prompt) is measured as
    the first-turn cache_creation_input_tokens. Multiplying it by the run's
    turn count gives an upper-bound estimate of how many cache_read tokens it
    would cost if every turn re-cached the entire baseline. This typically
    over-counts (cache TTL, partial cache hits), so we report both the naive
    estimate and the actual cache_read for comparison.
    """
    rows = []
    sums = {"baseline": 0, "amplified": 0, "actual_cache_read": 0}
    for r in runs:
        u = r.usage
        cache_read = u.get("cache_read_input_tokens", 0)
        baseline = r.baseline_tokens
        amplified = baseline * r.total_turns
        rows.append(
            {
                "exec_id": r.exec_id,
                "role": r.role,
                "baseline_tokens": baseline,
                "turns": r.total_turns,
                "baseline_amplified": amplified,
                "actual_cache_read": cache_read,
            }
        )
        sums["baseline"] += baseline
        sums["amplified"] += amplified
        sums["actual_cache_read"] += cache_read

    share = 100 * sums["amplified"] / max(1, sums["actual_cache_read"])

    if as_json:
        print(
            json.dumps(
                {"per_role": rows, "totals": sums, "baseline_share_pct": share},
                indent=2,
            )
        )
        return

    print(
        f"{'ROLE':<28} {'TURNS':>5} {'BASELINE':>10} {'AMPLIFIED':>12} "
        f"{'CACHE_READ':>12}"
    )
    for row in rows:
        print(
            f"{row['role']:<28} {row['turns']:>5} {row['baseline_tokens']:>10,} "
            f"{row['baseline_amplified']:>12,} {row['actual_cache_read']:>12,}"
        )
    print(
        f"\nSum baselines: {sums['baseline']:,}\n"
        f"Sum amplified baselines: {sums['amplified']:,}\n"
        f"Sum actual cache_read: {sums['actual_cache_read']:,}\n"
        f"Naive baseline share of cache_read: {share:.0f}% "
        f"(over-counts when cache TTL expires)"
    )


# ---------- driver ----------

SUBCOMMANDS = {
    "summary": cmd_summary,
    "tools": cmd_tools,
    "overlap": cmd_overlap,
    "warmup": cmd_warmup,
    "discovery": cmd_discovery,
    "baseline": cmd_baseline,
}


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Analyze ateam stream.jsonl logs for token consumption and overlap."
    )
    parser.add_argument(
        "command",
        nargs="?",
        default="all",
        choices=list(SUBCOMMANDS) + ["all"],
        help="Subcommand to run (default: all).",
    )
    parser.add_argument(
        "exec_ids",
        nargs="*",
        type=int,
        help="Exec IDs to analyze. Default: every id under --logs-dir.",
    )
    parser.add_argument(
        "--logs-dir",
        default=".ateam/logs",
        help="Path to the logs directory (default ./.ateam/logs).",
    )
    parser.add_argument("--top", type=int, default=20, help='Cap "top N" lists.')
    parser.add_argument(
        "--warmup-window",
        type=int,
        default=10,
        help="First-N-calls window for `warmup` (default 10).",
    )
    parser.add_argument("--json", action="store_true", help="Emit JSON instead of tables.")
    args = parser.parse_args()

    logs_dir = Path(args.logs_dir).resolve()
    if not logs_dir.is_dir():
        print(f"error: logs dir not found: {logs_dir}", file=sys.stderr)
        return 2

    exec_ids = args.exec_ids or discover_exec_ids(logs_dir)
    if not exec_ids:
        print(f"error: no runs found under {logs_dir}", file=sys.stderr)
        return 2

    runs: list[Run] = []
    for eid in exec_ids:
        r = load_run(eid, logs_dir)
        if r is None:
            print(f"warning: skipping exec {eid} (no stream.jsonl)", file=sys.stderr)
            continue
        # Skip runs that never produced a result event (canceled/in-progress)
        if r.result_event is None:
            print(
                f"warning: skipping exec {eid} ({r.role}): no result event",
                file=sys.stderr,
            )
            continue
        runs.append(r)

    if not runs:
        print("error: no analyzable runs", file=sys.stderr)
        return 2

    commands = list(SUBCOMMANDS) if args.command == "all" else [args.command]
    for i, name in enumerate(commands):
        if not args.json and len(commands) > 1:
            print(f"\n{'=' * 8}  {name.upper()}  {'=' * 8}\n")
        fn = SUBCOMMANDS[name]
        if name == "warmup":
            fn(runs, args.top, args.json, args.warmup_window)
        else:
            fn(runs, args.top, args.json)

    return 0


if __name__ == "__main__":
    sys.exit(main())
