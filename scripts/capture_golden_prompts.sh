#!/usr/bin/env bash
# Capture `ateam prompt --inline-paths` outputs for a representative role
# set into a snapshot directory, so a before/after diff can verify the
# v1 refactor preserved per-section content. Run twice (before + after
# the change you want to verify), then `diff -r <before> <after>`.
#
# Usage:
#   scripts/capture_golden_prompts.sh <out_dir> [project_dir]
#
#   out_dir      where to write per-role snapshots
#   project_dir  defaults to . (where .ateam/ is)
#
# The role set is hardcoded here — edit if your verification set is
# different. Picks one representative role per common axis:
#   - security      (single-segment dotted-free)
#   - code.bugs     (dotted role)
#   - test.gaps     (dotted role with .gaps suffix)
#   - testing_basic (underscore role, ships a code prompt too)
#
# Also captures the three singleton supervisor prompts and the auto-roles
# prompt. Outputs land under <out_dir>/<role>.report.txt etc.

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <out_dir> [project_dir]" >&2
  exit 2
fi

OUT_DIR="$1"
PROJECT_DIR="${2:-.}"

mkdir -p "$OUT_DIR"

BIN="${ATEAM:-ateam}"
if ! command -v "$BIN" >/dev/null 2>&1; then
  echo "error: $BIN not found in PATH; set ATEAM=path/to/ateam or add it to PATH" >&2
  exit 1
fi

ROLES=(security code.bugs test.gaps testing_basic)
SUPERVISOR_ACTIONS=(review verify code)

cd "$PROJECT_DIR"

for role in "${ROLES[@]}"; do
  for action in report code; do
    out="$OUT_DIR/$role.$action.txt"
    if "$BIN" prompt --role "$role" --action "$action" --inline-paths >"$out" 2>"$out.stderr"; then
      echo "captured: $out"
    else
      echo "skipped (no $action for $role): $out" >&2
      rm -f "$out"
    fi
  done
done

for action in "${SUPERVISOR_ACTIONS[@]}"; do
  out="$OUT_DIR/supervisor.$action.txt"
  if "$BIN" prompt --supervisor --action "$action" --inline-paths >"$out" 2>"$out.stderr"; then
    echo "captured: $out"
  else
    echo "skipped (failed): $out" >&2
    rm -f "$out"
  fi
done

echo
echo "Done. To diff before/after:"
echo "  diff -r <previous-snapshot> $OUT_DIR"
