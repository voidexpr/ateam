#!/usr/bin/env bash
set -euo pipefail

reviewer="codex-tmux"
coder="claude"

usage() {
  cat <<HELP
Usage: $(basename "$0") [--restart] [--help] [CUSTOM ...]

Two-agent loop: codex (via tmux + /review) reviews, claude implements.
  Round 1 — $reviewer runs /review on CUSTOM, writes codex_review.md.
  Round 2 — $coder applies findings or pushes back in writing,
            then builds + tests and commits if green.

Reports land in .ateam/shared/codex_reviews_claude_codes/.

Arguments:
  CUSTOM         Substituted verbatim into the /review command. Examples:
                   $(basename "$0") HEAD
                   $(basename "$0") "the last 3 commits"
                   $(basename "$0") "internal/runner/"
                 When omitted, an empty scope is passed.
                 Ignored when round 1 is being resumed from cache.

Options:
  --restart      Force a fresh run: timestamp-rename existing reports
                 even if the previous run did not complete.
  -h, --help     Show this message and exit.

Resume: each step writes one report file. On re-run, any step whose file is
already non-empty is skipped. The final step writes ${coder}'s report, so
when that file exists the previous run is treated as complete: both reports
are timestamp-renamed and the script starts fresh. Use --restart to force
the same reset mid-pipeline.
HELP
}

restart=false
args=()
while [ $# -gt 0 ]; do
  case "$1" in
    --restart) restart=true; shift ;;
    -h|--help) usage; exit 0 ;;
    --)        shift; while [ $# -gt 0 ]; do args+=("$1"); shift; done ;;
    *)         args+=("$1"); shift ;;
  esac
done

custom="${args[*]:-}"

base_dir=".ateam/shared/codex_reviews_claude_codes"
mkdir -p "$base_dir"
reviewer_out="$base_dir/codex_review.md"
coder_out="$base_dir/claude_response.md"

backup() {
  [ -f "$1" ] || return 0
  cp -p "$1" "${1%.md}.$(date +%Y-%m-%d_%H%M%S).md"
  rm -f "$1"
}

step() {
  printf '\n\033[1;36m── %s ──\033[0m\n' "$1"
}

maybe_run() {
  local out="$1" agent="$2" label="$3"
  if [ -s "$out" ]; then
    step "$label by $agent — cached ($out), skipping"
    return 0
  fi
  step "$label by $agent"
  ateam exec --agent "$agent"
}

if [ "$restart" = true ] || [ -s "$coder_out" ]; then
  for f in "$reviewer_out" "$coder_out"; do backup "$f"; done
fi

echo "Reviewer: $reviewer"
echo "   Coder: $coder"
echo "  Custom: $custom"

maybe_run "$reviewer_out" "$reviewer" "Round 1: review" <<EOF
/review $custom . Write your review to $reviewer_out
EOF

maybe_run "$coder_out" "$coder" "Round 2: respond" <<EOF
You are the coder. The reviewer wrote findings to $reviewer_out.
Apply the fixes you agree with; push back in writing on the ones you don't.

After your changes, build and test the project using whatever commands this
codebase conventionally uses (check the README, CLAUDE.md/AGENTS.md, Makefile,
or equivalent). Record the exact commands you ran and their results.

If build and tests are successful, do a git commit according to the project's standards.

Write a single report to $coder_out with three sections:
  1. Applied — what you fixed and why.
  2. Pushed back — what you rejected and why.
  3. Tests — exact command(s) run and pass/fail counts.
EOF

step "done"

ateam ps --limit 2
echo ""
cat "$coder_out"
