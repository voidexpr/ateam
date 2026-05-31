#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<HELP
Usage: $(basename "$0") [options]

Two-stage blackbox testing pass on recent changes.

Stage 1 (tester): an agent reviews recent work (uncommitted + last few
commits), writes tests that exercise the new/changed behavior, and KEEPS
failing tests in place with a written rationale. Does not touch production
code, does not commit.

Stage 2 (fixer): a second agent reads the tester's report, runs the tests,
and for each failing test makes a deliberate call — fix the code, fix the
test, or push back. Commits the kept tests plus any code fixes as a single
commit with a descriptive subject.

Options:
  --testing-agent AGENT  --agent for the tester  (default: ateam's default)
  --coding-agent  AGENT  --agent for the fixer   (default: ateam's default)
  --focus DESCRIPTION    override the default "recent commits" scope with a
                         freeform focus area (e.g. "the auth system, look for
                         session-handling bugs"). Ignored when resuming.
  -h, --help             show this help

Artifacts (per session, TS = run start time):
  .ateam/shared/blackbox-testing/<TS>/tests.md   tester's report
  .ateam/shared/blackbox-testing/<TS>/fixes.md   fixer's decisions
  .ateam/shared/blackbox-testing/latest          symlink to the latest <TS>/

Resume:
  If the latest session has tests.md but no fixes.md, the script reuses it
  and skips Stage 1. Delete fixes.md to force the fixer to re-run; delete
  the whole latest/ to start fresh.
HELP
}

testing_agent=""
coding_agent=""
focus=""

while [ $# -gt 0 ]; do
  case "$1" in
    --testing-agent) testing_agent="${2:?--testing-agent requires a value}"; shift 2 ;;
    --coding-agent)  coding_agent="${2:?--coding-agent requires a value}"; shift 2 ;;
    --focus)         focus="${2:?--focus requires a value}"; shift 2 ;;
    -h|--help)       usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ ! -d ".ateam" ]; then
  echo "blackbox-testing: run from a directory containing .ateam/" >&2
  exit 2
fi

if ! git rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "blackbox-testing: not inside a git repository" >&2
  exit 2
fi

shared_root=".ateam/shared/blackbox-testing"
mkdir -p "$shared_root"

# Resume if the latest session has tests.md but no fixes.md (Stage 1 done,
# Stage 2 incomplete). Otherwise start a fresh session.
resume=0
if [ -L "$shared_root/latest" ]; then
  prev_target="$(readlink "$shared_root/latest" || true)"
  if [ -n "$prev_target" ]; then
    prev_session="$shared_root/$prev_target"
    if [ -s "$prev_session/tests.md" ] && [ ! -s "$prev_session/fixes.md" ]; then
      resume=1
      ts="$prev_target"
      session_dir="$prev_session"
    fi
  fi
fi

if [ "$resume" -eq 0 ]; then
  ts="$(date +%Y-%m-%d_%H%M%S)"
  session_dir="$shared_root/$ts"
  mkdir -p "$session_dir"
  rm -f "$shared_root/latest"
  ln -s "$ts" "$shared_root/latest"
fi

tests_file="$session_dir/tests.md"
fixes_file="$session_dir/fixes.md"

step() {
  printf '\n── %s ──\n' "$1"
}

if [ "$resume" -eq 1 ]; then
  echo "blackbox-testing: resuming session=$ts (tests.md present, fixes.md missing)"
  if [ -n "$focus" ]; then
    echo "blackbox-testing: --focus is ignored when resuming — Stage 1 already ran"
  fi
else
  echo "blackbox-testing: session=$ts dir=$session_dir"
  if [ -n "$focus" ]; then
    echo "blackbox-testing: focus=$focus"
  fi
fi

if [ "$resume" -eq 0 ]; then
  if [ -n "$focus" ]; then
    scope_block="# Focus

You are testing this specific area: $focus

Use git history (\`git log -n 8 --stat\`, \`git diff\`) and the relevant
docs (README, CHANGELOG, area-specific docs) as context, but the focus
area above is the primary target — not whatever happens to be in recent
commits. Read the relevant code, identify the surface a user / caller
actually touches, and write tests against that surface."
  else
    scope_block="# Scope

Uncommitted work plus the last few commits on the current branch. Use
\`git status\`, \`git diff\`, \`git log -n 8 --stat\`, and per-commit diffs
to map what was added or changed — including any newly-documented behavior
in README / CHANGELOG / docs."
  fi

  step "Stage 1: blackbox tester writes tests"

  ateam exec --action blackbox-test \
    ${testing_agent:+--agent "$testing_agent"} <<EOF
You are a blackbox tester. Your job is to write tests that exercise the
target area below — happy paths, edge cases, error modes, documented
behavior — and to surface real bugs by KEEPING failing tests in place with
a clear rationale. You do NOT modify production code, you do NOT commit.

$scope_block

# Prefer end-to-end / black-box tests

Lean toward tests that exercise the real surface a user touches:
- CLI projects: invoke the actual binary as a subprocess and assert on
  exit code, stdout, stderr, files written.
- web servers / frontends: drive the running service over HTTP, and where
  the project already uses Playwright (or a comparable browser harness),
  drive a real browser flow.
- libraries: prefer integration tests across a real call path over deep
  unit tests of internal helpers.

Drop down to unit tests only when:
- the e2e harness for the relevant subsystem doesn't exist yet — record
  it as an infrastructure gap below, THEN drop a level.
- the bug clearly lives in a self-contained pure function where a unit
  test gives faster, cleaner signal.
- running e2e for this specific case would be wildly slower than the
  value it adds.

# How to write tests

Discover the project's testing conventions before writing anything:
- CLAUDE.md, AGENTS.md, README, Makefile, package.json scripts, and
  existing test files in the area you're touching.
- Pick the right tier for quick feedback. If e2e tests already exist for
  this area, use that harness.

Place new tests where similar tests already live; match the project's
naming and structure conventions. Run the tests as you go.

Cover:
- happy path on the target surface
- documented behavior that lacks a test
- input boundaries (empty, null, very large, malformed)
- error paths and the messages they surface
- concurrency / ordering / lifecycle hazards if relevant
- cross-component contracts the code is supposed to uphold

KEEP failing tests in place when you believe the code is wrong. A failing
test that demonstrates a real bug is more valuable than a passing test
that hides it. Do not skip, xfail, or comment out a test to make the suite
green.

If the test infrastructure for the kind of test you want to write is
missing (no harness for some subsystem, no fixture, no integration runner),
record the gap in the report rather than inventing an inline shim.

# Report

Write your report to: $tests_file

Use this structure:

  ## Summary
  - N tests added (X passing, Y failing)
  - top risks surfaced by this pass

  ## Tests added
  Per test file you created or modified:
  - path
  - what change/behavior it covers
  - tier (e2e | integration | unit) and why this tier
  - pass/fail status
  - if failing: one paragraph on why the test is correct and what the code
    appears to be doing wrong

  ## Infrastructure gaps
  Tests you wanted to write but couldn't, and what would be needed to make
  them writable (especially e2e harnesses you wished existed).

  ## Test commands
  Exact command(s) used, with their results.

# Hard constraints

- Do NOT modify production code, only tests and test fixtures/helpers.
- Do NOT git commit. The next stage commits your tests together with any
  code fixes.
- Do NOT skip, disable, or weaken existing tests to make the suite green.
EOF

  if [ ! -s "$tests_file" ]; then
    echo "blackbox-testing: tester did not write $tests_file — stopping before fixer." >&2
    exit 1
  fi
else
  step "Stage 1 skipped — reusing tests.md from $session_dir"
fi

step "Stage 2: fixer reviews tests and applies code fixes"

ateam exec --action blackbox-fix \
  ${coding_agent:+--agent "$coding_agent"} <<EOF
A blackbox tester has written tests against recent changes in this repo.
Their tests are already present in the working tree (uncommitted). Their
report lives at: $tests_file

Your job is to make a deliberate call on each test — fix the code, fix the
test, or push back — and then commit the result.

# Process

1. Read the tester's report end to end.
2. Run the test suite they used. Confirm which tests pass and which fail.
3. For each FAILING test:
   - Genuine bug in code -> apply the smallest correct fix. If the fix is
     large, risky, or clearly out of scope, note it and stop — do NOT
     ship a sweeping refactor disguised as a test fix.
   - Test is wrong (bad assumption, wrong API, flaky construction) -> fix
     or remove the test with a written reason.
   - Behavior is intended but undocumented -> fix the test AND add a brief
     note to the relevant doc/docstring so the next reader doesn't trip on
     it.
4. For PASSING tests added by the tester: keep them. Prune only if a test
   is clearly vacuous (asserts nothing meaningful, tautological mock
   round-trip). Do not rewrite for style.
5. After fixes, re-run the relevant tests until clean — or until you can
   defensibly explain why a remaining failure should stay.

# Report

Write your decisions to: $fixes_file

Use this structure:

  ## Summary
  - N tests reviewed; M code fixes applied; K tests removed/rewritten;
    P pushed back to the tester
  - final test command + pass/fail

  ## Per-test decisions
  Per test from the tester's report:
  - test name / path
  - action: CODE_FIX | TEST_FIX | TEST_REMOVED | KEPT_PASSING | PUSHED_BACK
  - one-paragraph rationale; cite file:line for any code change

  ## Pushbacks
  Tests where you disagreed with the tester's premise — what they thought
  was a bug, why it actually isn't, and what (if anything) you did instead.

# Commit

Create ONE git commit containing the kept tests plus any code fixes. The
subject line should summarize what was found and fixed concretely, e.g.:

  fix off-by-one in range parser; add 4 regression tests
  blackbox tests: 6 regression tests added, no bugs found

Do not push.
EOF

step "Done"
echo "Tester report: $tests_file"
echo "Fixer report:  $fixes_file"
echo "Latest:        $shared_root/latest"
echo "Latest commit:"
git log -1 --format='  %h %s (%cr)'
