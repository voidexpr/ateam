#!/usr/bin/env bash
set -euo pipefail

# TODO: test validation

# TODO: support effort / profile, etc ...
review_agent="codex"
coding_agent="claude"

agent_a_review="agent_${review_agent}_review_r1.md"
agent_b_review="agent_${coding_agent}_review_r1.md"

agent_a_review_r2="agent_${review_agent}_review_r2.md"
agent_b_review_r2="agent_${coding_agent}_review_r2.md"

echo "Agent A (Review Agent): $review_agent - report: $agent_a_review"
echo "Agent B (Coding Agent): $coding_agent - report: $agent_b_review"

function ateam-exec {
  ateam exec --dry-run "$@"
}

function cp-backup {
    [ -f "$1" ] && cp -pc "$1" "${1%.*}.$(date +%Y-%m-%d_%H%M%S).${1##*.}"
}

cp-backup "$agent_a_review_r1" && unlink "$agent_a_review_r1"
cp-backup "$agent_b_review_r1" && unlink "$agent_b_review_r1"
cp-backup "$agent_a_review_r2" && unlink "$agent_a_review_r2"
cp-backup "$agent_b_review_r2" && unlink "$agent_b_review_r2"

echo "------------------ INITIAL REVIEW ---------------------------------"
echo "
  Perform a critical code review of the recently committed changes, write your findings in $agent_a_review_r1
  Guidelines:
  - be skeptical, make your own opinion
  - don't be nitpicky: look for clear potential bugs and clear code structure issues that will hurt the addition of new features
  - consider test coverage: make the coder accountable for testing its new features
  - look for new code breaking existing features, maybe the coder didn't properly check the impact of their changes
" | ateam-exec --agent $review_agent

echo "------------------ FIRST FIXES ---------------------------------"
echo "
  Here is a code review of your recent changes: $agent_a_review_r1.
  Review these findings, make appropriate fixes and push back with an explanation if you don't agree.
  Write your report to $agent_b_review_r1
" | ateam-exec --agent $coding_agent

echo "------------------ RE-REVIEW ---------------------------------"
echo "
* You've submitted the following review to another agent: $agent_a_review_r1
* This is their findings and fixes: $agent_b_review_r1

Guidelines:
* did they incorrectly reject your findings ? should you provide more information or agree with the push-back ?

Write your current assessment of what needs to be done at: $agent_a_review_r2 (ok to overwrite)
" | ateam-exec --agent $review_agent

echo "------------------ RE-FIX ---------------------------------"
echo "
* You've submitted the following review and fixes to another agent: $agent_b_review_r1
* This is their findings: $agent_a_review_r2

Perform the recommended tasks or clearly state why they should not be done.
Write to $agent_b_review_r2 and clearly summarize it in your last message
" | ateam-exec --agent $coding_agent
echo "done"
