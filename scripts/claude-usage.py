#!/usr/bin/env python3
"""
claude-usage — Report Claude Code subscription usage limits.

Hits the undocumented Anthropic OAuth endpoint that Claude Code itself uses
internally.

Commands:
    cat      dump the raw API JSON to stdout (default if omitted)
    check    pretty-print the relevant utilization info
    sleep    if any specified threshold is exceeded, sleep until the 5-hour reset

All commands accept the same options:
    --alert-5h-max PCT   exit 5 if 5-hour window utilization exceeds PCT
    --alert-7d-max PCT   exit 7 if 7-day window utilization exceeds PCT
    --cache-file PATH    cache the API response to PATH
    --cache-ttl DUR      reuse cache if mtime within DUR (<N>{s,m,d})

PCT may include a trailing '%' for readability.

Exit codes:
    0  ok (or no thresholds specified)
    5  --alert-5h-max threshold exceeded (cat/check only)
    7  --alert-7d-max threshold exceeded (cat/check only)
    1  error fetching data

Caveats:
  - Uses the OAuth token from Claude Code's credential store. Per Anthropic's
    terms (Feb 2026), reusing this token in third-party tools is technically
    a ToS violation. Use at your own risk.
  - The endpoint is undocumented and could change without notice.

TODO: use the official way once it's released (claude usage ? claude limits ?)
    see: https://github.com/anthropics/claude-code/issues/13585
"""

import argparse
import json
import platform
import re
import subprocess
import sys
import time
import urllib.request
import urllib.error
from datetime import datetime, timezone
from pathlib import Path

API_URL = "https://api.anthropic.com/api/oauth/usage"
USER_AGENT = "claude-code/2.1.140"
BETA_HEADER = "oauth-2025-04-20"

COMMANDS = ("cat", "check", "sleep")


def get_token_macos():
    try:
        out = subprocess.run(
            ["security", "find-generic-password", "-s", "Claude Code-credentials", "-w"],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
        return json.loads(out)["claudeAiOauth"]["accessToken"]
    except (subprocess.CalledProcessError, json.JSONDecodeError, KeyError) as e:
        sys.exit(f"error: could not read token from macOS Keychain: {e}")


def get_token_linux():
    path = Path.home() / ".claude" / ".credentials.json"
    if not path.exists():
        sys.exit(f"error: {path} not found — are you logged in to Claude Code?")
    try:
        with open(path) as f:
            return json.load(f)["claudeAiOauth"]["accessToken"]
    except (json.JSONDecodeError, KeyError) as e:
        sys.exit(f"error: could not parse {path}: {e}")


def get_token():
    if platform.system() == "Darwin":
        return get_token_macos()
    return get_token_linux()


def fetch_usage(token):
    req = urllib.request.Request(
        API_URL,
        headers={
            "Authorization": f"Bearer {token}",
            "anthropic-beta": BETA_HEADER,
            "User-Agent": USER_AGENT,
            "Accept": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        sys.exit(f"error: HTTP {e.code} — {body}")
    except urllib.error.URLError as e:
        sys.exit(f"error: network failure — {e.reason}")


def parse_pct(s):
    """Parse a percent threshold, accepting an optional trailing '%'."""
    try:
        return float(s.rstrip("%").strip())
    except ValueError:
        raise argparse.ArgumentTypeError(f"invalid percent value: {s!r}")


def parse_duration(s):
    """Parse a duration string like '30s', '5m', '1d' into seconds."""
    m = re.fullmatch(r"\s*(\d+)\s*([smd])\s*", s)
    if not m:
        sys.exit(f"error: invalid --cache-ttl '{s}' (expected number followed by s/m/d)")
    n, unit = int(m.group(1)), m.group(2)
    return n * {"s": 1, "m": 60, "d": 86400}[unit]


def parse_iso(s):
    if not s:
        return None
    try:
        return datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None


def fmt_local(value):
    """Format an ISO timestamp string or datetime as local-time 'YYYY-MM-DD HH:MM:SS'."""
    if isinstance(value, str):
        dt = parse_iso(value)
        if dt is None:
            return value
    else:
        dt = value
    return dt.astimezone().strftime("%Y-%m-%d %H:%M:%S")


def read_cache(path, ttl_seconds):
    """Return cached data if file exists and is fresh, else None."""
    p = Path(path)
    if not p.exists():
        return None
    if time.time() - p.stat().st_mtime > ttl_seconds:
        return None
    try:
        with open(p) as f:
            return json.load(f)
    except (json.JSONDecodeError, OSError):
        return None


def write_cache(path, data):
    p = Path(path)
    tmp = p.with_suffix(p.suffix + ".tmp")
    with open(tmp, "w") as f:
        json.dump(data, f)
    tmp.replace(p)


def utilization_pct(window):
    """Extract utilization from a window dict, normalized to 0-100."""
    if not window:
        return None
    pct = float(window.get("utilization", 0))
    if pct <= 1.0:
        pct *= 100
    return pct


def load_data(args):
    """Load usage data, honoring --cache-file / --cache-ttl.

    Returns (data, cache_status) where cache_status is:
      - None if --cache-file was not provided
      - "hit"  if cache was fresh and used
      - "miss" if cache was stale/missing and we refetched
    """
    if (args.cache_file is None) != (args.cache_ttl is None):
        sys.exit("error: --cache-file and --cache-ttl must be used together")

    if args.cache_file:
        data = read_cache(args.cache_file, parse_duration(args.cache_ttl))
        if data is not None:
            return data, "hit"

    data = fetch_usage(get_token())
    if args.cache_file:
        write_cache(args.cache_file, data)
        return data, "miss"
    return data, None


def cache_status_line(args, status):
    """One-line summary of how the cache was used. None if no cache configured."""
    if status is None:
        return None
    p = Path(args.cache_file)
    mtime = fmt_local(datetime.fromtimestamp(p.stat().st_mtime, tz=timezone.utc))
    if status == "hit":
        return f"cache: {p} (used, updated {mtime})"
    return f"cache: {p} (refetched, updated {mtime})"


def threshold_breaches(args, data):
    """List of (label, value_pct, limit_pct, exit_code) for each exceeded threshold."""
    checks = (
        ("5-hour", "five_hour", args.alert_5h_max, 5),
        ("7-day", "seven_day", args.alert_7d_max, 7),
    )
    breaches = []
    for label, key, limit, code in checks:
        if limit is None:
            continue
        pct = utilization_pct(data.get(key))
        if pct is not None and pct > limit:
            breaches.append((label, pct, limit, code))
    return breaches


def threshold_exit_code(args, data):
    """Return 5/7 if a threshold is exceeded, else 0."""
    breaches = threshold_breaches(args, data)
    return breaches[0][3] if breaches else 0


def cmd_cat(args, data, cache_status):
    print(json.dumps(data))
    return threshold_exit_code(args, data)


def cmd_check(args, data, cache_status):
    line = cache_status_line(args, cache_status)
    if line:
        print(line)
    for label, key in (("5-hour", "five_hour"), ("7-day", "seven_day"), ("7-day opus", "seven_day_opus")):
        window = data.get(key)
        if not window:
            continue
        pct = utilization_pct(window) or 0.0
        reset = window.get("resets_at")
        print(f"{label:11} {pct:5.1f}%  resets {fmt_local(reset) if reset else '?'}")
    for label, pct, limit, _ in threshold_breaches(args, data):
        print(f"WARNING: {label} window at {pct:.1f}% exceeds threshold {limit:.1f}%")
    return threshold_exit_code(args, data)


def cmd_sleep(args, data, cache_status):
    if threshold_exit_code(args, data) == 0:
        return 0
    reset = (data.get("five_hour") or {}).get("resets_at")
    dt = parse_iso(reset)
    if not dt:
        sys.exit("error: cannot determine 5-hour reset time")
    secs = (dt - datetime.now(timezone.utc)).total_seconds()
    if secs > 0:
        print(f"sleeping {int(secs)}s until {fmt_local(dt)}", file=sys.stderr)
        time.sleep(secs)
    return 0


COMMAND_HANDLERS = {"cat": cmd_cat, "check": cmd_check, "sleep": cmd_sleep}


def main():
    argv = sys.argv[1:]
    if not argv or argv[0] not in COMMANDS:
        argv = ["cat"] + argv

    parser = argparse.ArgumentParser(description="Report Claude Code usage.")
    parser.add_argument("command", choices=COMMANDS, help="action to perform (default: cat)")
    parser.add_argument("--alert-5h-max", type=parse_pct, default=None,
                        help="threshold for 5-hour window utilization (e.g. 80 or 80%%)")
    parser.add_argument("--alert-7d-max", type=parse_pct, default=None,
                        help="threshold for 7-day window utilization (e.g. 90 or 90%%)")
    parser.add_argument("--cache-file", default=None,
                        help="path to JSON cache file; used with --cache-ttl")
    parser.add_argument("--cache-ttl", default=None,
                        help="cache TTL as <N>{s,m,d} (e.g. 30s, 5m, 1d)")
    args = parser.parse_args(argv)

    data, cache_status = load_data(args)
    sys.exit(COMMAND_HANDLERS[args.command](args, data, cache_status))


if __name__ == "__main__":
    main()
