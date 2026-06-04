#!/usr/bin/env bash
set -euo pipefail

base_dir=".ateam/shared/double_review"
codex_file="$base_dir/codex_code_review.md"
claude_file="$base_dir/claude_code_review.md"
final_file="$base_dir/coding_report.md"

codex_agent="codex-tmux"
claude_agent="claude-high"
coding_agent="claude-high"
mode="high"
focus="recent changes"

usage() {
  cat <<HELP
Usage: $(basename "$0") [options]

Two parallel reviews (codex with /review + claude code with /code-review $mode)
of recent changes, then a merge step that applies the fixes both agree on and
pushes back on the rest.
Reviews are cached; --force re-runs them. The two review files are
timestamp-archived at the end so the next run starts fresh; the merged
$final_file is kept.

Options:
  --focus TEXT                       What the reviews should target (default: "$focus").
                                     Substituted into both review prompts.
  --force                            Re-run reviews even if cached.
  --codex-agent NAME                 Override codex review agent (default: $codex_agent).
  --claude-agent NAME                Override claude review agent (default: $claude_agent).
  --coding-agent NAME                Override merge/coding agent     (default: $coding_agent).
  --claude-review-effort EFFORT      low | medium | high | xhigh | max (default: $mode).
  -h, --help                         Show this message.
HELP
}

force=false
while [ $# -gt 0 ]; do
  case "$1" in
    --focus)                 focus="${2:?--focus needs a value}"; shift 2 ;;
    --force)                 force=true; shift ;;
    --codex-agent)           codex_agent="${2:?--codex-agent needs a value}"; shift 2 ;;
    --claude-agent)          claude_agent="${2:?--claude-agent needs a value}"; shift 2 ;;
    --coding-agent)          coding_agent="${2:?--coding-agent needs a value}"; shift 2 ;;
    --claude-review-effort)  mode="${2:?--claude-review-effort needs a value}"; shift 2 ;;
    -h|--help)               usage; exit 0 ;;
    *)                       echo "Unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$mode" in
  low|medium|high|xhigh|max) ;;
  *) echo "Invalid --claude-review-effort: $mode (want low|medium|high|xhigh|max)" >&2; exit 2 ;;
esac

mkdir -p "$base_dir"

backup() {
  [ -f "$1" ] || return 0
  cp -p "$1" "${1%.md}.$(date +%Y-%m-%d_%H%M%S).md"
  rm -f "$1"
}

step() { printf '\n\033[1;36m── %s ──\033[0m\n' "$1"; }

if [ "$force" = true ]; then
  backup "$codex_file"
  backup "$claude_file"
fi

codex_pid=""
if [ ! -s "$codex_file" ]; then
  step "codex review ($codex_agent) → $codex_file"
  ateam exec --agent "$codex_agent" --action "code-review" --role codex <<EOF &
/review $focus, write results to $codex_file
EOF
  codex_pid=$!
else
  step "codex review cached, skipping"
fi

claude_pid=""
if [ ! -s "$claude_file" ]; then
  step "claude review ($claude_agent, $mode) → $claude_file"
  ateam exec --agent "$claude_agent" --action "code-review" --role claude <<EOF &
/code-review $mode --fix $focus, write results to $claude_file
EOF
  claude_pid=$!
else
  step "claude review cached, skipping"
fi

[ -n "$codex_pid" ]  && wait "$codex_pid"
[ -n "$claude_pid" ] && wait "$claude_pid"

step "merge + implement ($coding_agent) → $final_file"
ateam exec --agent "$coding_agent" --action "code-review" --role code <<EOF
Review $codex_file and $claude_file.

Consolidate findings and:
* fix all valid issues
* document rejected findings with clear justification

Follow CLAUDE.md build/test conventions:
* run the deepest available validation for the current environment
* do not configure missing tooling or services

If validation succeeds, create a project-standard git commit.
Write concise reasoning and actions to $final_file.
EOF

step "done"
ateam ps --limit 3
cat "$final_file"

# Archive reviews so the next run recomputes them; keep the merged report.
backup "$codex_file"
backup "$claude_file"
