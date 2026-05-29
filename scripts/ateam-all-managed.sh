#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<HELP
Usage: $(basename "$0") [options]

Wrap \`ateam all\` so a transient failure doesn't kill an overnight run.

On failure, spawn a troubleshooter agent that:
  - finds the failed exec(s) via \`ateam ps\`
  - debugs them via \`ateam inspect EXEC_ID --auto-debug\`
  - optionally fixes a small in-scope problem (.ateam/, .ateamorg/, cwd)
  - writes a JSON verdict to a known path

The script reads the verdict, optionally sleeps (API_ERROR / OS_RESOURCES),
then resumes from the exact phase the verdict picks — re-running only the
report roles that failed, not the ones that already succeeded.

Options:
  --max-attempts N           total attempts including the first one (default 3)
  --sleep-api SECONDS        sleep on sleep_reason=API_ERROR     (default 300)
  --sleep-os SECONDS         sleep on sleep_reason=OS_RESOURCES  (default 600)
  --troubleshoot-agent NAME  --agent passed to the troubleshooter exec
  --troubleshoot-profile X   --profile passed to the troubleshooter exec
  --simulate JSON            smoke-test mode: skip real ateam invocations and the
                             troubleshooter LLM call, use the given verdict JSON
                             directly, force attempt 1 to "fail" and attempt 2 to
                             "succeed". Use to verify the phase-chain wiring
                             without spending tokens.
  -h, --help                 show this help

Artifacts (per session, TS = run start time):
  .ateam/shared/ateam-managed-all/<TS>/manager.log         dual echo of this script
  .ateam/shared/ateam-managed-all/<TS>/action-try-<N>.json troubleshoot verdict

The troubleshoot exec is also visible via \`ateam ps\` with action=troubleshoot.
HELP
}

max_attempts=3
sleep_api=300
sleep_os=600
troubleshoot_agent=""
troubleshoot_profile=""
simulate_verdict=""

while [ $# -gt 0 ]; do
  case "$1" in
    --max-attempts)         max_attempts="${2:?--max-attempts requires a value}"; shift 2 ;;
    --sleep-api)            sleep_api="${2:?--sleep-api requires a value}"; shift 2 ;;
    --sleep-os)             sleep_os="${2:?--sleep-os requires a value}"; shift 2 ;;
    --troubleshoot-agent)   troubleshoot_agent="${2:?--troubleshoot-agent requires a value}"; shift 2 ;;
    --troubleshoot-profile) troubleshoot_profile="${2:?--troubleshoot-profile requires a value}"; shift 2 ;;
    --simulate)             simulate_verdict="${2:?--simulate requires a JSON verdict}"; shift 2 ;;
    -h|--help)              usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

for v in max_attempts sleep_api sleep_os; do
  if ! [[ "${!v}" =~ ^[0-9]+$ ]]; then
    echo "ateam-all-managed: --${v//_/-} must be a non-negative integer (got '${!v}')" >&2
    exit 2
  fi
done
if [ "$max_attempts" -lt 1 ]; then
  echo "ateam-all-managed: --max-attempts must be >= 1" >&2
  exit 2
fi

if [ ! -d ".ateam" ]; then
  echo "ateam-all-managed: run from a directory containing .ateam/" >&2
  exit 2
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "ateam-all-managed: jq is required to parse the troubleshoot verdict" >&2
  exit 2
fi

if [ -n "$simulate_verdict" ]; then
  if ! jq empty <<<"$simulate_verdict" >/dev/null 2>&1; then
    echo "ateam-all-managed: --simulate value is not valid JSON" >&2
    exit 2
  fi
fi

ts="$(date +%Y-%m-%d_%H%M%S)"
session_dir=".ateam/shared/ateam-managed-all/$ts"
mkdir -p "$session_dir"
logfile="$session_dir/manager.log"

# Dual echo: this script's stdout+stderr go to the terminal AND to manager.log.
# Uses a FIFO instead of process substitution so it works under restricted
# shells / sandboxes that block /dev/fd/* writes.
logpipe="$session_dir/.manager.log.pipe"
rm -f "$logpipe"
mkfifo "$logpipe"
tee -a "$logfile" < "$logpipe" &
tee_pid=$!
trap 'exec 1>&- 2>&-; wait "$tee_pid" 2>/dev/null; rm -f "$logpipe"' EXIT
exec >"$logpipe" 2>&1

step() {
  printf '\n── %s ──\n' "$1"
}

echo "ateam-all-managed: session=$ts dir=$session_dir"
echo "ateam-all-managed: max_attempts=$max_attempts sleep_api=${sleep_api}s sleep_os=${sleep_os}s"
if [ -n "$simulate_verdict" ]; then
  echo "ateam-all-managed: SIMULATE mode — no real ateam invocations, no LLM call"
  echo "ateam-all-managed: simulated verdict: $simulate_verdict"
fi

troubleshoot_prompt() {
  local out_path="$1"
  cat <<EOF
The most recent ateam pipeline phase just failed. Decide whether a plain
rerun will succeed, whether a small in-scope fix will unblock it, or whether
the issue is out of scope and the run should abort.

# Investigate

1. \`ateam ps\` — identify recent runs with non-zero exit / error status.
   The most recent batch corresponds to the phase that just failed.
2. \`ateam inspect EXEC_ID [EXEC_ID...] --auto-debug\` for each failed exec.
   Read the debug output carefully.
3. \`ateam env\` if helpful.

# Classify the cause

- API_ERROR        provider transient (5xx, 529, rate limit, quota); wait + retry
- OS_RESOURCES     host pressure (OOM, EAGAIN, disk full, sustained high CPU)
- FIXED_ISSUE      you fixed a config/file problem in the allowed scope; retry now
- TRANSIENT_OTHER  nothing wrong with host or provider, looks like a flake
- UNFIXABLE        persistent or out of scope; do not retry

# Allowed fix scope (FIXED_ISSUE only)

Editable directories:
  - cwd and its subdirectories
  - .ateam/
  - .ateamorg/

Allowed git: local commands only — no push/pull/fetch.

NOT allowed: Claude or Codex config outside .ateamorg/ (e.g. \$HOME/.claude,
\$HOME/.codex). Host-level config is out of scope — classify UNFIXABLE.

Prefer renaming over deletion. Audit any delete carefully.

# Decide where to resume

Pick the earliest phase that needs to re-run. Pipeline order is:
  report -> review -> code -> verify

  rerun_phase=report   one or more role reports failed; list the failed roles
  rerun_phase=review   reports all succeeded, review failed
  rerun_phase=code     review succeeded, code failed
  rerun_phase=verify   code succeeded, verify failed
  rerun_phase=all      unsure or multiple phases broken; start over

When rerun_phase=report, rerun_roles MUST be a non-empty array of the role
names that need re-running (as they appear in \`ateam roles\`, e.g.
["code.bugs","test.gaps"]). For any other phase, rerun_roles MUST be null.

# Emit the verdict

Write valid JSON to: $out_path

Schema (exact field names, no extras):

  {
    "rerun":        true | false,
    "rerun_phase":  "report" | "review" | "code" | "verify" | "all" | null,
    "rerun_roles":  ["role.name", ...] | null,
    "sleep_reason": "API_ERROR" | "OS_RESOURCES" | null,
    "cause":        "API_ERROR" | "OS_RESOURCES" | "FIXED_ISSUE" | "TRANSIENT_OTHER" | "UNFIXABLE",
    "summary":      "one paragraph plain text — what happened and what you did"
  }

Mapping rules (must match):
  cause=API_ERROR        -> rerun=true,  sleep_reason="API_ERROR"
  cause=OS_RESOURCES     -> rerun=true,  sleep_reason="OS_RESOURCES"
  cause=FIXED_ISSUE      -> rerun=true,  sleep_reason=null
  cause=TRANSIENT_OTHER  -> rerun=true,  sleep_reason=null
  cause=UNFIXABLE        -> rerun=false, sleep_reason=null, rerun_phase=null, rerun_roles=null

After writing the file, print one confirmation line and stop.
EOF
}

run_troubleshoot() {
  local out_path="$1"
  troubleshoot_prompt "$out_path" | ateam exec \
    --action troubleshoot \
    ${troubleshoot_agent:+--agent "$troubleshoot_agent"} \
    ${troubleshoot_profile:+--profile "$troubleshoot_profile"} \
    -
}

# Run the smallest phase chain that covers everything from $1 onward.
# $2 is the comma-separated role list (only used when phase=report).
run_phase_chain() {
  local phase="$1"
  local roles="${2:-}"
  case "$phase" in
    all)
      ateam all
      ;;
    report)
      if [ -z "$roles" ]; then
        echo "ateam-all-managed: rerun_phase=report needs rerun_roles" >&2
        return 2
      fi
      ateam report --roles "$roles" \
        && ateam review \
        && ateam code \
        && ateam verify
      ;;
    review)
      ateam review && ateam code && ateam verify
      ;;
    code)
      ateam code && ateam verify
      ;;
    verify)
      ateam verify
      ;;
    *)
      echo "ateam-all-managed: unknown phase '$phase'" >&2
      return 2
      ;;
  esac
}

# First attempt is plain `ateam all`. Subsequent attempts come from the verdict.
next_phase="all"
next_roles=""

for attempt in $(seq 1 "$max_attempts"); do
  if [ "$next_phase" = "report" ] && [ -n "$next_roles" ]; then
    step "Attempt $attempt/$max_attempts: ateam report --roles $next_roles -> review -> code -> verify"
  else
    step "Attempt $attempt/$max_attempts: phase=$next_phase"
  fi

  if [ -n "$simulate_verdict" ]; then
    if [ "$next_phase" = "report" ] && [ -n "$next_roles" ]; then
      echo "[simulated] would run: ateam report --roles $next_roles && ateam review && ateam code && ateam verify"
    elif [ "$next_phase" = "all" ]; then
      echo "[simulated] would run: ateam all"
    elif [ "$next_phase" = "verify" ]; then
      echo "[simulated] would run: ateam verify"
    else
      echo "[simulated] would run: ateam $next_phase && (subsequent phases)"
    fi
    if [ "$attempt" -eq 1 ]; then
      echo "[simulated] forcing failure to exercise troubleshoot path"
      phase_ok=1
    else
      echo "[simulated] forcing success"
      phase_ok=0
    fi
  else
    if run_phase_chain "$next_phase" "$next_roles"; then phase_ok=0; else phase_ok=1; fi
  fi

  if [ "$phase_ok" -eq 0 ]; then
    step "Pipeline succeeded on attempt $attempt"
    exit 0
  fi

  if [ "$attempt" -ge "$max_attempts" ]; then
    echo
    echo "ateam-all-managed: max attempts ($max_attempts) reached, giving up." >&2
    exit 1
  fi

  action_file="$session_dir/action-try-$attempt.json"
  step "Attempt $attempt failed — running troubleshooter (verdict -> $action_file)"

  if [ -n "$simulate_verdict" ]; then
    echo "[simulated] writing provided verdict to $action_file (no LLM call)"
    printf '%s\n' "$simulate_verdict" > "$action_file"
  else
    if ! run_troubleshoot "$action_file"; then
      echo "ateam-all-managed: troubleshooter agent itself failed; giving up." >&2
      exit 1
    fi
  fi

  if [ ! -s "$action_file" ]; then
    echo "ateam-all-managed: troubleshooter did not write $action_file; giving up." >&2
    exit 1
  fi

  if ! jq empty "$action_file" >/dev/null 2>&1; then
    echo "ateam-all-managed: $action_file is not valid JSON; giving up." >&2
    exit 1
  fi

  rerun=$(jq -r        '.rerun // false'            "$action_file")
  cause=$(jq -r        '.cause // "UNFIXABLE"'      "$action_file")
  next_phase=$(jq -r   '.rerun_phase // "all"'      "$action_file")
  next_roles=$(jq -r '
    if (.rerun_roles // null) == null
    then ""
    else (.rerun_roles | join(","))
    end' "$action_file")
  sleep_reason=$(jq -r '.sleep_reason // ""'        "$action_file")
  summary=$(jq -r      '.summary // ""'             "$action_file")

  echo
  echo "Troubleshoot verdict (attempt $attempt):"
  echo "  cause        = $cause"
  echo "  rerun        = $rerun"
  echo "  rerun_phase  = $next_phase"
  echo "  rerun_roles  = ${next_roles:-(none)}"
  echo "  sleep_reason = ${sleep_reason:-(none)}"
  echo "  verdict file = $action_file"
  echo "  summary      = $summary"

  if [ "$rerun" != "true" ]; then
    echo
    echo "ateam-all-managed: troubleshooter said do not rerun (cause=$cause). Stopping." >&2
    exit 1
  fi

  case "$next_phase" in
    all|report|review|code|verify) ;;
    *)
      echo "ateam-all-managed: invalid rerun_phase '$next_phase', falling back to 'all'" >&2
      next_phase="all"
      next_roles=""
      ;;
  esac

  if [ "$next_phase" = "report" ] && [ -z "$next_roles" ]; then
    echo "ateam-all-managed: rerun_phase=report but no rerun_roles; falling back to 'all'" >&2
    next_phase="all"
  fi

  case "$sleep_reason" in
    API_ERROR)    sleep_for="$sleep_api" ;;
    OS_RESOURCES) sleep_for="$sleep_os"  ;;
    ""|null)      sleep_for=0 ;;
    *)
      echo "ateam-all-managed: unknown sleep_reason '$sleep_reason', not sleeping." >&2
      sleep_for=0
      ;;
  esac

  if [ "$sleep_for" -gt 0 ]; then
    if [ -n "$simulate_verdict" ]; then
      step "[simulated] would sleep ${sleep_for}s (sleep_reason=$sleep_reason)"
    else
      step "Sleeping ${sleep_for}s before retry (sleep_reason=$sleep_reason)"
      sleep "$sleep_for"
    fi
  fi
done
