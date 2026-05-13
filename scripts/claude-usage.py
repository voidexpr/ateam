#!/usr/bin/env python3
"""
claude-usage — Report Claude Code subscription usage limits as JSON.

Hits the undocumented Anthropic OAuth endpoint that Claude Code itself uses
internally. Outputs the raw API response as JSON to stdout.

Usage:
    claude-usage
    claude-usage --alert-5h-max 80
    claude-usage --alert-7d-max 90
    claude-usage --alert-5h-max 80 --alert-7d-max 90
    claude-usage --cache-file /tmp/usage.json --cache-ttl 30s

Exit codes:
    0  ok (or no thresholds specified)
    5  --alert-5h-max threshold exceeded
    7  --alert-7d-max threshold exceeded (only if 5h not exceeded)
    1  error fetching data

Caveats:
  - Uses the OAuth token from Claude Code's credential store. Per Anthropic's
    terms (Feb 2026), reusing this token in third-party tools is technically
    a ToS violation. Use at your own risk.
  - The endpoint is undocumented and could change without notice.
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
from pathlib import Path

API_URL = "https://api.anthropic.com/api/oauth/usage"
USER_AGENT = "claude-code/2.1.140"
BETA_HEADER = "oauth-2025-04-20"


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


def main():
    parser = argparse.ArgumentParser(description="Report Claude Code usage as JSON.")
    parser.add_argument("--alert-5h-max", type=parse_pct, default=None,
                        help="exit 5 if 5-hour window utilization exceeds this percent (e.g. 80 or 80%%)")
    parser.add_argument("--alert-7d-max", type=parse_pct, default=None,
                        help="exit 7 if 7-day window utilization exceeds this percent (e.g. 90 or 90%%)")
    parser.add_argument("--cache-file", default=None,
                        help="path to JSON cache file; used with --cache-ttl")
    parser.add_argument("--cache-ttl", default=None,
                        help="cache TTL as <N>{s,m,d} (e.g. 30s, 5m, 1d)")
    args = parser.parse_args()

    if (args.cache_file is None) != (args.cache_ttl is None):
        sys.exit("error: --cache-file and --cache-ttl must be used together")

    data = None
    if args.cache_file:
        data = read_cache(args.cache_file, parse_duration(args.cache_ttl))

    if data is None:
        token = get_token()
        data = fetch_usage(token)
        if args.cache_file:
            write_cache(args.cache_file, data)

    print(json.dumps(data))

    if args.alert_5h_max is not None:
        pct = utilization_pct(data.get("five_hour"))
        if pct is not None and pct > args.alert_5h_max:
            sys.exit(5)
    if args.alert_7d_max is not None:
        pct = utilization_pct(data.get("seven_day"))
        if pct is not None and pct > args.alert_7d_max:
            sys.exit(7)


if __name__ == "__main__":
    main()
