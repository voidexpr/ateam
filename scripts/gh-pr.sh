#!/usr/bin/env bash
set -euo pipefail

# Two-step GitHub PR feedback workflow driven by `ateam exec`.
#
#   review-feedback PRID — read PR feedback (inline review comments,
#     review summaries, PR-level comments) via gh and write a
#     recommendation report (APPLY / ENHANCE / PUSH BACK) to
#     .ateam/gh-pr/PRID/review-feedback.md.
#
#   do-feedback PRID — read that report, apply the code changes,
#     reply on GitHub for pushed-back items, run the project's
#     build/tests, commit per-finding, and push to the PR branch.

agent="claude"

usage() {
  cat <<HELP
Usage: $(basename "$0") [--restart] <subcommand> <PRID>

Subcommands:
  review-feedback PRID  Read PR feedback; write recommendations to
                        .ateam/gh-pr/PRID/review-feedback.md
  do-feedback PRID      Read the report, apply changes, reply on GitHub
                        for push-backs, commit, push; log to
                        .ateam/gh-pr/PRID/do-feedback.md

Options:
  --restart   Timestamp-rename the existing report(s) for this PRID
              before starting, even if mid-pipeline.
  -h, --help  Show this message and exit.

Resume: review-feedback skips if its report is already non-empty.
do-feedback always runs (its log is overwritten each time).

Requires: gh (authenticated), git remote pointing at the PR repo.
HELP
}

restart=false
positional=()
while [ $# -gt 0 ]; do
  case "$1" in
    --restart) restart=true; shift ;;
    -h|--help) usage; exit 0 ;;
    --)        shift; while [ $# -gt 0 ]; do positional+=("$1"); shift; done ;;
    -*)        echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
    *)         positional+=("$1"); shift ;;
  esac
done

if [ ${#positional[@]} -lt 2 ]; then
  usage >&2
  exit 2
fi

subcommand="${positional[0]}"
prid="${positional[1]}"

case "$subcommand" in
  review-feedback|do-feedback) ;;
  *) echo "Unknown subcommand: $subcommand" >&2; usage >&2; exit 2 ;;
esac

if ! command -v gh >/dev/null 2>&1; then
  echo "Error: gh CLI not found on PATH" >&2
  exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
  echo "Error: gh is not authenticated (run: gh auth login)" >&2
  exit 1
fi

base_dir=".ateam/shared/gh-pr/$prid"
mkdir -p "$base_dir"
review_out="$base_dir/review-feedback.md"
do_log="$base_dir/do-feedback.md"

backup() {
  [ -f "$1" ] || return 0
  cp -p "$1" "${1%.md}.$(date +%Y-%m-%d_%H%M%S).md"
  rm -f "$1"
}

step() {
  printf '\n\033[1;36m── %s ──\033[0m\n' "$1"
}

run_review() {
  if [ "$restart" = true ]; then
    backup "$review_out"
  fi
  if [ -s "$review_out" ]; then
    step "review-feedback PR #$prid — cached ($review_out), skipping"
    return 0
  fi
  step "review-feedback PR #$prid by $agent"
  ateam exec --agent "$agent" <<EOF
You are triaging the review feedback on GitHub PR #$prid for this repo.

Use the gh CLI to gather (the repo is inferred from the current git remote):
  - PR metadata:        gh pr view $prid --json title,body,state,headRefName,baseRefName,url,isDraft
  - The diff:           gh pr diff $prid
  - Review summaries:   gh api -X GET "repos/{owner}/{repo}/pulls/$prid/reviews" --paginate
  - Inline comments:    gh api -X GET "repos/{owner}/{repo}/pulls/$prid/comments" --paginate
  - PR-level comments:  gh api -X GET "repos/{owner}/{repo}/issues/$prid/comments" --paginate

Skip resolved threads and comments authored by the PR author.

Write $review_out. For each piece of remaining feedback, emit one section:
  ### <short title> — <reviewer login> [file:line if inline]
  > <exact quote of the comment>

  **Recommendation:** APPLY | ENHANCE | PUSH BACK

  **Reasoning:** one to three sentences. For APPLY, sketch the change.
  For ENHANCE, name the original ask + what to add beyond it.
  For PUSH BACK, give a clearly-argued counter grounded in the code or
  prior decisions — not a vague "I disagree".

End with a one-paragraph summary: counts of APPLY / ENHANCE / PUSH BACK
and any cross-cutting themes worth flagging to the reviewer.
EOF
}

run_do() {
  if [ ! -s "$review_out" ]; then
    echo "Error: $review_out is missing or empty." >&2
    echo "  Run: $(basename "$0") review-feedback $prid" >&2
    exit 1
  fi
  step "do-feedback PR #$prid by $agent"
  ateam exec --agent "$agent" <<EOF
You are applying review feedback to GitHub PR #$prid. The recommendation
report is at $review_out — treat it as the plan.

For each section:
  - APPLY:     make the change in code.
  - ENHANCE:   apply the original suggestion AND the enhancement.
  - PUSH BACK: do NOT change code. Reply on GitHub with the argument
               from the report. Prefer replying in-thread to the
               original comment (gh api -X POST
               "repos/{owner}/{repo}/pulls/$prid/comments/{COMMENT_ID}/replies"
               -f body="..."). Fall back to gh pr comment $prid -b "..."
               only when the item isn't tied to a specific thread.

After the code changes:
  - Build and test using this project's conventional commands (check
    README, CLAUDE.md/AGENTS.md, Makefile). Record exact commands.
  - Commit per-finding when reasonable; group changes to the same
    file. Use commit subjects that reference the finding briefly.
  - Push to the PR branch (the current branch should already be the
    PR head — verify with: gh pr view $prid --json headRefName).

Write $do_log (overwriting OK) with:
  1. Applied — finding → commit SHA + file(s).
  2. Enhanced — finding → what was added beyond the original ask.
  3. Pushed back — finding → GitHub reply text and where it landed.
  4. Tests — exact commands run and pass/fail counts.

End with a one-line summary: counts + push status.
EOF
}

case "$subcommand" in
  review-feedback) run_review ;;
  do-feedback)     run_do ;;
esac

step "done"
