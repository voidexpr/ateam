#!/usr/bin/env bash
set -euo pipefail

CACHE_FILE="/tmp/model_prices_and_context_window.json"
CACHE_MAX_AGE_DAYS=7
PRICING_URL="https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

usage() {
  cat <<'EOF'
Usage:
  pricing-cost MODEL INPUT_TOKENS OUTPUT_TOKENS

Example:
  pricing-cost gpt-4o-mini 1200 300

Notes:
  - MODEL must be the base model name, without a dated suffix.
  - Cost is computed as:
      input_tokens  * input_cost_per_token
    + output_tokens * output_cost_per_token
EOF
}

err() {
  echo "Error: $*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || err "missing required command: $1"
}

refresh_cache_if_needed() {
  local now mtime age_secs max_age_secs tmp

  max_age_secs=$((CACHE_MAX_AGE_DAYS * 24 * 60 * 60))
  now="$(date +%s)"

  if [[ -f "$CACHE_FILE" ]]; then
    if stat -f %m "$CACHE_FILE" >/dev/null 2>&1; then
      # macOS / BSD stat
      mtime="$(stat -f %m "$CACHE_FILE")"
    else
      # GNU stat
      mtime="$(stat -c %Y "$CACHE_FILE")"
    fi

    age_secs=$((now - mtime))
    if (( age_secs < max_age_secs )); then
      return 0
    fi
  fi

  tmp="${CACHE_FILE}.tmp.$$"
  curl -fsSL "$PRICING_URL" -o "$tmp"
  mv "$tmp" "$CACHE_FILE"
}

list_valid_models() {
  jq -r '
    to_entries
    | map(select(
        (.value.input_cost_per_token? != null) and
        (.value.output_cost_per_token? != null)
      ))
    | map(.key)
    | map(select(test("^[^/]+-[0-9]{4}-[0-9]{2}-[0-9]{2}$") | not))
    | sort
    | .[]
  ' "$CACHE_FILE"
}

compute_cost() {
  local model="$1"
  local input_tokens="$2"
  local output_tokens="$3"

  jq -r --arg model "$model" --argjson inTok "$input_tokens" --argjson outTok "$output_tokens" '
    .[$model] as $m
    | if $m == null then
        empty
      elif ($m.input_cost_per_token? == null or $m.output_cost_per_token? == null) then
        empty
      else
        (($inTok * $m.input_cost_per_token) + ($outTok * $m.output_cost_per_token))
      end
  ' "$CACHE_FILE"
}

is_integer() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

main() {
  need_cmd curl
  need_cmd jq

  [[ $# -eq 3 ]] || { usage; exit 1; }

  local model="$1"
  local input_tokens="$2"
  local output_tokens="$3"
  local cost

  is_integer "$input_tokens" || err "INPUT_TOKENS must be a non-negative integer"
  is_integer "$output_tokens" || err "OUTPUT_TOKENS must be a non-negative integer"

  refresh_cache_if_needed

  cost="$(compute_cost "$model" "$input_tokens" "$output_tokens" || true)"

  if [[ -z "${cost:-}" || "$cost" == "null" ]]; then
    echo "Unknown or unsupported model: $model" >&2
    echo >&2
    echo "Valid models:" >&2
    list_valid_models >&2
    exit 2
  fi

  printf "model=%s input_tokens=%s output_tokens=%s total_cost_usd=%.8f\n" \
    "$model" "$input_tokens" "$output_tokens" "$cost"
}

main "$@"