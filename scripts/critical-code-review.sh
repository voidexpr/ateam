#!/usr/bin/env bash
set -euo pipefail

# Two-agent critical code-review loop driven by `ateam exec`.
#
#   Round 1 — reviewer audits the latest commit, coder applies fixes.
#   Round 2 — reviewer assesses the response (rejections, regressions),
#             coder finalizes.
#
# Demonstrates how to compose a short multi-agent workflow with the
# `ateam exec --agent` primitive. Reports land as agent_*_r{1,2}.md in
# the working directory; prior runs are timestamp-backed up.

reviewer="codex"
coder="claude"

reviewer_r1="agent_${reviewer}_r1.md"
coder_r1="agent_${coder}_r1.md"
reviewer_r2="agent_${reviewer}_r2.md"
coder_r2="agent_${coder}_r2.md"

backup() {
  [ -f "$1" ] || return 0
  cp -p "$1" "${1%.md}.$(date +%Y-%m-%d_%H%M%S).md"
  rm -f "$1"
}

step() {
  printf '\n\033[1;36m── %s ──\033[0m\n' "$1"
}

run() {
  local agent="$1"
  step "$2"
  ateam exec --agent "$agent"
}

for f in "$reviewer_r1" "$coder_r1" "$reviewer_r2" "$coder_r2"; do backup "$f"; done

echo "Reviewer: $reviewer  Coder: $coder"

run "$reviewer" "Round 1 — review" <<EOF
You are the reviewer. Critically audit the diff of HEAD (the most recent commit) and write
your findings to $reviewer_r1. The coder ($coder) will respond.

- Be skeptical, form your own opinion.
- Skip nits. Surface clear bugs and structural choices that will hurt future changes.
- Flag missing test coverage for new code paths.
- Check that the change doesn't silently break existing features.
EOF

run "$coder" "Round 1 — fixes" <<EOF
You are the coder. The reviewer ($reviewer) wrote findings to $reviewer_r1.
Apply the fixes you agree with; push back in writing on the ones you don't.

After your changes, build and test the project using whatever commands this
codebase conventionally uses (check the README, CLAUDE.md/AGENTS.md, Makefile,
or equivalent). Record the exact commands you ran and their results.

Write a single report to $coder_r1 with three sections:
  1. Applied — what you fixed and why.
  2. Pushed back — what you rejected and why.
  3. Tests — exact command(s) run and pass/fail counts.
EOF

run "$reviewer" "Round 2 — re-review" <<EOF
You are the reviewer. Your round-1 findings are in $reviewer_r1; the coder's response is in $coder_r1.

- Did they incorrectly reject any finding? Provide more evidence or concede.
- Did the fixes introduce regressions? Spot-check the diff since your first review.
- Are the test runs in $coder_r1 credible (right command for this project, real output)?

Write your updated assessment to $reviewer_r2 (overwriting OK).
EOF

run "$coder" "Round 2 — final fixes" <<EOF
You are the coder. The reviewer's follow-up is in $reviewer_r2; your prior response is in $coder_r1.

Apply remaining recommendations or state precisely why each should not be done.
Re-run the same build and test commands you used in round 1 and record the results.
Write to $coder_r2 with the same three-section structure as before, and end your
final message with a one-paragraph summary (counts of applied vs. pushed back + test result).
EOF

step "done"
