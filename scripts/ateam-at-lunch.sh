#!/usr/bin/env bash
set -euo pipefail

# Long lunch break? Park ateam on a side branch and let it tidy, refactor,
# and add tests while you're away. Returns the worktree's HEAD commit and an
# `ateam serve` hint at the end so you can review what happened.
#
# Flow:
#   1. Stash pending changes in the source repo (if any).
#   2. Create or reuse a worktree on the `ateam-lunch` branch at
#      ../<repo>-ateam-lunch (override the parent with --worktree-parent).
#   3. Build + test (the agent figures out the right commands from CLAUDE.md /
#      Makefile / etc.). If anything fails, the agent fixes it on the branch.
#   4. ateam exec /simplify; re-test.
#   5. ateam all --roles code.verify_recent,code.refactor_recent,test.add_recent;
#      re-test.
#   6. Print the worktree's HEAD commit and where to run `ateam serve`.
#
# On error or success: cd back to the original directory, and if a stash was
# taken, remind the user to pop it.

worktree_parent="${ATEAM_LUNCH_PARENT:-..}"
branch="ateam-lunch"

original_dir="$(pwd)"
stash_msg=""
worktree_path=""

usage() {
  cat <<HELP
Usage: $(basename "$0") [--worktree-parent DIR] [--help]

Run a lunchtime ateam tidy-and-refactor cycle on a side branch.

Options:
  --worktree-parent DIR  Parent directory for the worktree (default: ..).
                         The worktree lands at DIR/<repo>-${branch}.
                         Override with the ATEAM_LUNCH_PARENT env var too.
  -h, --help             Show this message and exit.
HELP
}

while [ $# -gt 0 ]; do
  case "$1" in
    --worktree-parent) worktree_parent="${2:?--worktree-parent requires an argument}"; shift 2 ;;
    -h|--help)         usage; exit 0 ;;
    *)                 echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

step() {
  printf '\n\033[1;36m── %s ──\033[0m\n' "$1"
}

on_exit() {
  local rc=$?
  cd "$original_dir"
  if [ "$rc" -ne 0 ]; then
    echo >&2
    echo "ERROR: ateam-at-lunch failed (exit $rc)." >&2
    if [ -n "$worktree_path" ] && [ -d "$worktree_path" ]; then
      echo "The worktree is intact for inspection:" >&2
      echo "  cd $worktree_path" >&2
      echo "  ateam serve     # open the review UI from there" >&2
    fi
  fi
  if [ -n "$stash_msg" ]; then
    echo
    echo "Heads up: pending changes were stashed at the start of this run."
    echo "  Stash entry: $stash_msg"
    echo "  Restore with: (cd $original_dir && git stash pop)"
  fi
}
trap on_exit EXIT

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "Not inside a git repository — run from a checkout." >&2
  exit 1
fi

# 1. Stash any pending source-repo changes so the current checkout stays clean
#    while ateam works on the side branch.
if [ -n "$(git status --porcelain)" ]; then
  stash_msg="ateam-lunch auto-stash $(date +%Y-%m-%d_%H%M%S)"
  git stash push --include-untracked -m "$stash_msg"
  step "Stashed source-repo changes: $stash_msg"
fi

# 2. Create or reuse the worktree.
repo="$(basename "$(git rev-parse --show-toplevel)")"
parent_abs="$(cd "$worktree_parent" && pwd)"
worktree_path="$parent_abs/${repo}-${branch}"

if [ -d "$worktree_path" ]; then
  step "Reusing worktree at $worktree_path"
else
  step "Creating worktree at $worktree_path on $branch"
  if git show-ref --verify --quiet "refs/heads/$branch"; then
    git worktree add "$worktree_path" "$branch"
  else
    git worktree add -b "$branch" "$worktree_path"
  fi
fi

cd "$worktree_path"
echo "Working in: $(pwd)"

test_guide="ateam-at-lunch-how-to-test.md"
function write-testing-guide {
   ateam exec --cheaper-model <<EOF
Find out how to run tests for this project.
- Read CLAUDE.md, build system files and other potential development documentation
- Only look at the top level and if nothing found look one level down
Document:
- the test command(s) to quickly check code
- the more thorough test commands and when to use them
Write it as a markdown file called $test_guide
EOF
}

# Reused for the three build+test passes. The agent finds the right commands;
# we don't bake any specific tool into the script so it works across stacks.
build_and_test_prompt='Build and run the project'\''s minimal / fast tests.
- Discover the right commands from CLAUDE.md, AGENTS.md, README, the Makefile,
  package.json scripts, or other build artefacts in this repo.
- If the project exposes multiple test tiers, pick the quickest tier that still
  exercises the recent code (skip docker / live-API / e2e tiers).
- Run the build first, then the tests. Record the exact commands and their
  pass/fail status.
- If anything fails, fix the code or the tests on this branch and commit the
  fix with a short, clear message. Re-run until clean or until you have a
  defensible reason it cannot be fixed in this session.
- End your message with one line: "Build: PASS/FAIL  Tests: PASS/FAIL  via <cmd>".'

step "Initial build + test"
ateam exec --agent claude <<<"$build_and_test_prompt"

step "ateam exec /code-review"
ateam exec --agent claude <<<"/code-review xhigh --fix for recent changes"

step "Re-test after /code-review"
ateam exec --agent claude <<<"$build_and_test_prompt"

step "ateam all (verify / refactor / add tests on recent changes)"
ateam all --roles code.verify_recent,code.refactor_recent,test.add_recent

step "Final build + test"
ateam exec --agent claude <<<"$build_and_test_prompt"

step "Lunch is over"
echo "Worktree HEAD:"
git log -1 --format='  %h %s (%cr)'
echo
echo "Review the session:"
echo "  cd $worktree_path && ateam serve"
