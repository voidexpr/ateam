#!/usr/bin/env bash
# CLI test: verify `ateam run ping --dry-run` clearly shows which auth
# method is active and which is masked across the 16 combinations of
# ateam secret store state x env var state.
#
# Matrix dimensions:
#   API   = ANTHROPIC_API_KEY
#   OAUTH = CLAUDE_CODE_OAUTH_TOKEN
#   store: none | API | OAUTH | API+OAUTH
#   env:   none | API | OAUTH | API+OAUTH
#
# Run: make test-cli

set -uo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
ATEAM=${ATEAM:-"$REPO_ROOT/ateam"}

if [ ! -x "$ATEAM" ]; then
    echo "ateam binary not found at $ATEAM — run 'make build' first" >&2
    exit 1
fi

# Isolated workspace — realpath resolves /tmp → /private/tmp on macOS
# so ateam's project-path validation accepts the relative path.
TMPROOT=$(realpath "$(mktemp -d -p "${TMPDIR:-/tmp}")")
trap 'rm -rf "$TMPROOT"' EXIT

PROJ="$TMPROOT/work/proj"
export HOME="$TMPROOT/home"
mkdir -p "$PROJ" "$HOME"
(cd "$PROJ" && "$ATEAM" init --org-create "$TMPROOT/work" >/dev/null 2>&1)

SECRETS_FILE="$PROJ/.ateam/secrets.env"

pass=0
fail=0
failures=()

# run_case <label> <store_spec> <env_spec> <pattern>...
# store_spec / env_spec: "" | API | OAUTH | API+OAUTH
run_case() {
    local label=$1 store=$2 envspec=$3; shift 3

    : > "$SECRETS_FILE"
    case "$store" in
        API)       echo "ANTHROPIC_API_KEY=store-apikey-value" > "$SECRETS_FILE" ;;
        OAUTH)     echo "CLAUDE_CODE_OAUTH_TOKEN=store-oauth-value" > "$SECRETS_FILE" ;;
        API+OAUTH) printf 'ANTHROPIC_API_KEY=store-apikey-value\nCLAUDE_CODE_OAUTH_TOKEN=store-oauth-value\n' > "$SECRETS_FILE" ;;
    esac

    local env_args=(HOME="$HOME" PATH="$PATH")
    case "$envspec" in
        API)       env_args+=(ANTHROPIC_API_KEY=env-apikey-value) ;;
        OAUTH)     env_args+=(CLAUDE_CODE_OAUTH_TOKEN=env-oauth-value) ;;
        API+OAUTH) env_args+=(ANTHROPIC_API_KEY=env-apikey-value CLAUDE_CODE_OAUTH_TOKEN=env-oauth-value) ;;
    esac

    local output
    output=$(cd "$PROJ" && env -i "${env_args[@]}" \
        "$ATEAM" run ping --agent claude --model haiku --dry-run 2>&1)

    local missing=()
    for expected in "$@"; do
        if ! printf '%s' "$output" | grep -qE "$expected"; then
            missing+=("$expected")
        fi
    done

    if [ ${#missing[@]} -eq 0 ]; then
        pass=$((pass+1))
        printf '  PASS  %s\n' "$label"
    else
        fail=$((fail+1))
        failures+=("$label")
        printf '  FAIL  %s\n' "$label"
        for m in "${missing[@]}"; do
            printf '          missing: %s\n' "$m"
        done
        printf '          output:\n'
        printf '%s\n' "$output" | sed 's/^/            /'
    fi
}

echo "Running 16 auth combination tests (store x env)..."
echo ""

# --- Row 1: store=none ---
run_case "01  store=none       env=none       : both not found" "" "" \
    'ANTHROPIC_API_KEY.*(not found|✗)' \
    'CLAUDE_CODE_OAUTH_TOKEN.*(not found|✗)'

run_case "02  store=none       env=API        : API active (env)" "" "API" \
    'ANTHROPIC_API_KEY.*active.*env' \
    'CLAUDE_CODE_OAUTH_TOKEN.*not found'

run_case "03  store=none       env=OAUTH      : OAUTH active (env)" "" "OAUTH" \
    'CLAUDE_CODE_OAUTH_TOKEN.*active.*env' \
    'ANTHROPIC_API_KEY.*not found'

run_case "04  store=none       env=API+OAUTH  : API active (env), OAUTH masked" "" "API+OAUTH" \
    'ANTHROPIC_API_KEY.*active.*env' \
    'CLAUDE_CODE_OAUTH_TOKEN.*stripped' \
    'Notice.*ANTHROPIC_API_KEY.*env.*ignore.*CLAUDE_CODE_OAUTH_TOKEN'

# --- Row 2: store=API ---
run_case "05  store=API        env=none       : API active (project)" "API" "" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*not found'

run_case "06  store=API        env=API        : API active (project)" "API" "API" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*not found'

run_case "07  store=API        env=OAUTH      : API active (project), OAUTH masked" "API" "OAUTH" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*stripped' \
    'Notice.*ANTHROPIC_API_KEY.*ateam secret.*ignore.*CLAUDE_CODE_OAUTH_TOKEN'

run_case "08  store=API        env=API+OAUTH  : API active (project), OAUTH masked" "API" "API+OAUTH" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*stripped' \
    'Notice.*ANTHROPIC_API_KEY.*ateam secret.*ignore.*CLAUDE_CODE_OAUTH_TOKEN'

# --- Row 3: store=OAUTH ---
run_case "09  store=OAUTH      env=none       : OAUTH active (project)" "OAUTH" "" \
    'CLAUDE_CODE_OAUTH_TOKEN.*active.*project' \
    'ANTHROPIC_API_KEY.*not found'

run_case "10  store=OAUTH      env=API        : OAUTH active (project), API masked" "OAUTH" "API" \
    'CLAUDE_CODE_OAUTH_TOKEN.*active.*project' \
    'ANTHROPIC_API_KEY.*stripped' \
    'Notice.*CLAUDE_CODE_OAUTH_TOKEN.*ateam secret.*ignore.*ANTHROPIC_API_KEY'

run_case "11  store=OAUTH      env=OAUTH      : OAUTH active (project)" "OAUTH" "OAUTH" \
    'CLAUDE_CODE_OAUTH_TOKEN.*active.*project' \
    'ANTHROPIC_API_KEY.*not found'

run_case "12  store=OAUTH      env=API+OAUTH  : OAUTH active (project), API masked" "OAUTH" "API+OAUTH" \
    'CLAUDE_CODE_OAUTH_TOKEN.*active.*project' \
    'ANTHROPIC_API_KEY.*stripped' \
    'Notice.*CLAUDE_CODE_OAUTH_TOKEN.*ateam secret.*ignore.*ANTHROPIC_API_KEY'

# --- Row 4: store=API+OAUTH ---
# When both are configured in the store, only the first alternative
# (ANTHROPIC_API_KEY) is used by IsolateCredentials; OAUTH is silently unused.
run_case "13  store=API+OAUTH  env=none       : API active (project)" "API+OAUTH" "" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*(stripped|unused|project)'

run_case "14  store=API+OAUTH  env=API        : API active (project)" "API+OAUTH" "API" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*(stripped|unused|project)'

run_case "15  store=API+OAUTH  env=OAUTH      : API active (project), OAUTH masked" "API+OAUTH" "OAUTH" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*stripped'

run_case "16  store=API+OAUTH  env=API+OAUTH  : API active (project), OAUTH masked" "API+OAUTH" "API+OAUTH" \
    'ANTHROPIC_API_KEY.*active.*project' \
    'CLAUDE_CODE_OAUTH_TOKEN.*stripped'

echo ""
echo "---"
echo "Result: $pass passed, $fail failed"
if [ $fail -gt 0 ]; then
    echo ""
    echo "Failures:"
    for f in "${failures[@]}"; do
        echo "  - $f"
    done
    exit 1
fi
