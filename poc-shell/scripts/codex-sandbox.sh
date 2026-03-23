#!/usr/bin/env bash
set -euo pipefail

SETTINGS_FILE="/tmp/codex-sandbox.settings.$(date +%Y-%m-%d_%H_%M_%S).$$.json"

web_fetch=true
always_approve=false
remote=false
one_shot=false
sandbox_verbose=false
extra_dirs=()

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") [wrapper-options] [codex-options...]

Wrapper options:
  --no-web-fetch      Disable web search support.
  --always-approve    Keep sandbox, but never prompt for approvals.
  --remote            Reserved for Claude compatibility (unsupported in Codex).
  --one-shot          Run codex non-interactively (codex exec --json).
  --sandbox-verbose   Verbose live event output in --one-shot mode.
  --path <dir>        Add extra writable directory (repeatable).
  --help              Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --no-web-fetch)
            web_fetch=false
            shift
            ;;
        --always-approve)
            always_approve=true
            shift
            ;;
        --remote)
            remote=true
            shift
            ;;
        --one-shot)
            one_shot=true
            shift
            ;;
        --sandbox-verbose)
            sandbox_verbose=true
            shift
            ;;
        --path)
            [[ -z "${2:-}" ]] && { echo "error: --path requires an argument" >&2; exit 1; }
            resolved="$(cd "$2" 2>/dev/null && pwd -P)" || { echo "error: path not found: $2" >&2; exit 1; }
            extra_dirs+=("$resolved")
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            break
            ;;
    esac
done

ensure_codex() {
    if command -v codex >/dev/null 2>&1; then
        return 0
    fi

    echo "codex CLI not found; attempting installation..." >&2

    if command -v brew >/dev/null 2>&1; then
        if brew install --cask codex; then
            command -v codex >/dev/null 2>&1 && return 0
        fi
    fi

    if command -v npm >/dev/null 2>&1; then
        if npm install -g @openai/codex; then
            command -v codex >/dev/null 2>&1 && return 0
        fi
    fi

    echo "error: failed to install codex CLI automatically." >&2
    echo "hint: install with Homebrew (macOS): brew install --cask codex" >&2
    echo "hint: or npm: npm install -g @openai/codex" >&2
    exit 1
}

ensure_codex

add_dirs=("$HOME/Library/Caches")
if [[ ${#extra_dirs[@]} -gt 0 ]]; then
    for d in "${extra_dirs[@]}"; do
        add_dirs+=("$d")
    done
fi

# Build JSON fragments for printing effective settings.
add_dir_json=""
for d in "${add_dirs[@]}"; do
    add_dir_json+=","$'\n'"      \"$d\""
done
add_dir_json="${add_dir_json#,}"

cat > "$SETTINGS_FILE" <<EOF
{
  "codex": {
    "sandbox": "workspace-write",
    "ask_for_approval": "$($always_approve && echo "never" || echo "on-request")",
    "dangerously_bypass_approvals_and_sandbox": false,
    "search_enabled": $($web_fetch && echo "true" || echo "false"),
    "add_directories": [
$(printf '%s\n' "$add_dir_json")
    ]
  },
  "requested_compatibility": {
    "remote": $($remote && echo "true" || echo "false"),
    "one_shot": $($one_shot && echo "true" || echo "false"),
    "sandbox_verbose": $($sandbox_verbose && echo "true" || echo "false")
  }
}
EOF

if [[ ${#extra_dirs[@]} -gt 0 ]]; then
    echo "Sandbox additional paths:" >&2
    for d in "${extra_dirs[@]}"; do
        echo "  + $d" >&2
    done
fi

echo "" >&2
echo "Custom Codex Sandbox Settings: $SETTINGS_FILE" >&2
cat "$SETTINGS_FILE" >&2
echo "codex specific args passed through: $*" >&2
echo >&2
echo "Behavior differences vs claude-sandbox:" >&2
echo "  - Codex has no Claude-style 'remote-control'; --remote is unsupported." >&2
echo "  - Codex has --search only (no separate WebFetch); --no-web-fetch disables web search entirely." >&2
echo "  - Claude JSON settings allow per-tool/per-domain policies; Codex CLI exposes coarser sandbox controls." >&2
echo "  - --one-shot monitoring is best-effort from 'codex exec --json' events, not Claude stream-json schema." >&2
echo >&2

codex_common_args=()
codex_common_args+=(--sandbox workspace-write)
$always_approve && codex_common_args+=(--ask-for-approval never) || codex_common_args+=(--ask-for-approval on-request)

$web_fetch && codex_common_args+=(--search)

for d in "${add_dirs[@]}"; do
    codex_common_args+=(--add-dir "$d")
done

if $one_shot; then
    if [[ $# -eq 0 ]]; then
        if [[ -t 0 ]]; then
            echo "error: --one-shot requires a prompt argument or stdin" >&2
            exit 1
        fi
        set -- "$(cat)"
    fi

    command -v jq >/dev/null 2>&1 || { echo "error: jq is required for --one-shot" >&2; exit 1; }

    STREAM_FILE="$(mktemp "${TMPDIR:-/tmp}/codex-oneshot-XXXXXX.jsonl")"
    STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/codex-oneshot-XXXXXX.log")"
    cleanup() { rm -f "$STREAM_FILE" "$STDERR_FILE"; }
    trap cleanup EXIT

    # Live monitor: tail the stream and print compact progress to stderr.
    live_monitor() {
        local start_ts=$SECONDS event_count=0 tool_count=0 text_count=0

        while [[ ! -f "$STREAM_FILE" ]]; do sleep 0.2; done

        # Use tail -n +1 -f to read from beginning, avoiding race with initial writes
        tail -n +1 -f "$STREAM_FILE" 2>/dev/null | while IFS= read -r line; do
            event_count=$((event_count + 1))
            local elapsed=$((SECONDS - start_ts))
            local ts
            ts=$(printf '%02d:%02d' $((elapsed / 60)) $((elapsed % 60)))

            local etype
            etype=$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null) || continue
            [[ -z "$etype" ]] && continue

            case "$etype" in
                turn.started|thread.started)
                    printf '\033[2m[%s] %s\033[0m\n' "$ts" "$etype" >&2
                    ;;
                exec_command_begin|web_search_begin|mcp_tool_call_begin|custom_tool_call_begin|patch_apply_begin|apply_patch_begin)
                    tool_count=$((tool_count + 1))
                    if $sandbox_verbose; then
                        printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m\n' "$ts" "$tool_count" "$etype" >&2
                        printf '%s' "$line" | jq -r '.' 2>/dev/null | sed 's/^/             /' >&2
                    else
                        local detail
                        detail=$(printf '%s' "$line" | jq -r '
                            (.command // .query // .tool_name // .name // empty) as $d
                            | if ($d | type) == "array" then ($d | join(" "))
                              elif ($d | type) == "string" then $d
                              else "" end
                        ' 2>/dev/null)
                        [[ ${#detail} -gt 80 ]] && detail="${detail:0:80}..."
                        if [[ -n "$detail" ]]; then
                            printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m \033[2m%s\033[0m\n' \
                                "$ts" "$tool_count" "$etype" "$detail" >&2
                        else
                            printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m\n' \
                                "$ts" "$tool_count" "$etype" >&2
                        fi
                    fi
                    ;;
                agent_message_delta|agent_message|assistant_message)
                    local text
                    text=$(printf '%s' "$line" | jq -r '
                        .delta // .text // .message // .content // empty
                    ' 2>/dev/null)
                    if [[ -n "$text" ]]; then
                        text_count=$((text_count + 1))
                        if $sandbox_verbose; then
                            printf '\033[33m[%s]\033[0m \033[2mtext #%d:\033[0m\n' "$ts" "$text_count" >&2
                            printf '%s\n' "$text" | sed 's/^/             /' >&2
                        else
                            local preview
                            preview=$(printf '%s' "$text" | tr '\n' ' ' | sed 's/  */ /g')
                            [[ ${#preview} -gt 80 ]] && preview="${preview:0:80}..."
                            printf '\033[33m[%s]\033[0m \033[2mtext #%d:\033[0m \033[2m%s\033[0m\n' \
                                "$ts" "$text_count" "$preview" >&2
                        fi
                    fi
                    ;;
                error)
                    local emsg
                    emsg=$(printf '%s' "$line" | jq -r '.message // "unknown error"' 2>/dev/null)
                    printf '\033[31m[%s] error\033[0m %s\n' "$ts" "$emsg" >&2
                    ;;
                turn.completed|turn.failed)
                    printf '\033[32m[%s] done\033[0m events=%d tools=%d type=%s\n' \
                        "$ts" "$event_count" "$tool_count" "$etype" >&2
                    break
                    ;;
                *)
                    if $sandbox_verbose; then
                        printf '\033[2m[%s] %s\033[0m\n' "$ts" "$etype" >&2
                    elif (( event_count % 20 == 0 )); then
                        printf '\033[2m[%s] events=%d tools=%d working...\033[0m\n' \
                            "$ts" "$event_count" "$tool_count" >&2
                    fi
                    ;;
            esac
        done
    }

    live_monitor &
    MONITOR_PID=$!

    codex "${codex_common_args[@]}" exec --json "$@" \
        >"$STREAM_FILE" 2>"$STDERR_FILE" || true

    sleep 0.5
    kill "$MONITOR_PID" 2>/dev/null
    wait "$MONITOR_PID" 2>/dev/null || true

    RESULT_JSON="$(jq -s '
        ([.[] | select(.type == "turn.completed")] | last)
        // ([.[] | select(.type == "turn.failed")] | last)
        // {}
    ' "$STREAM_FILE" 2>/dev/null)"

    RESULT_TYPE="$(printf '%s' "$RESULT_JSON" | jq -r '.type // "unknown"')"
    DURATION_MS="$(printf '%s' "$RESULT_JSON" | jq -r '.duration_ms // .durationMs // "?"')"

    if [[ "$DURATION_MS" != "?" && "$DURATION_MS" =~ ^[0-9]+$ ]]; then
        DURATION_S=$((DURATION_MS / 1000))
        DURATION_HUMAN="$((DURATION_S / 60))m $((DURATION_S % 60))s"
    else
        DURATION_HUMAN="?"
    fi

    INPUT_TOKENS="$(printf '%s' "$RESULT_JSON" | jq -r '.usage.input_tokens // .usage.inputTokens // "?"')"
    OUTPUT_TOKENS="$(printf '%s' "$RESULT_JSON" | jq -r '.usage.output_tokens // .usage.outputTokens // "?"')"

    ERROR_COUNT="$(jq -r 'select(.type == "error") | .message' "$STREAM_FILE" 2>/dev/null | wc -l | tr -d ' ')"
    EVENT_COUNT="$(wc -l < "$STREAM_FILE" | tr -d ' ')"

    TOOL_DIST="$(jq -r '
        if .type == "exec_command_begin" then "exec_command"
        elif .type == "web_search_begin" then "web_search"
        elif .type == "mcp_tool_call_begin" then "mcp_tool_call"
        elif .type == "custom_tool_call_begin" then "custom_tool_call"
        elif .type == "patch_apply_begin" or .type == "apply_patch_begin" then "apply_patch"
        else empty end
    ' "$STREAM_FILE" 2>/dev/null | sort | uniq -c | sort -rn)"

    AGENT_RESPONSE="$(jq -s '
        (
            [.[] | select(.type == "item.completed")
             | .item? | select(.type == "agent_message" or .type == "assistant_message")
             | (.text // ([.content[]? | .text? // empty] | join("\n")))]
            | map(select(. != null and . != ""))
            | last
        )
        // (
            [.[] | select(.type == "agent_message_delta") | .delta]
            | map(select(. != null and . != ""))
            | join("")
        )
        // empty
    ' "$STREAM_FILE" 2>/dev/null)"

    cat >&2 <<EOF

=== Run Summary ===

Result:     $RESULT_TYPE
Duration:   $DURATION_HUMAN
Events:     $EVENT_COUNT
Errors:     $ERROR_COUNT
Tokens:     input=$INPUT_TOKENS output=$OUTPUT_TOKENS

Tools used:
$TOOL_DIST
EOF

    if [[ -s "$STDERR_FILE" ]]; then
        echo "" >&2
        echo "=== Codex STDERR (tail) ===" >&2
        tail -n 30 "$STDERR_FILE" >&2
    fi

    if [[ -n "$AGENT_RESPONSE" && "$AGENT_RESPONSE" != "null" ]]; then
        echo "" >&2
        echo "=== Agent Response ===" >&2
        echo "" >&2
        printf '%s' "$AGENT_RESPONSE" | jq -r '.' >&2
    fi

    exit 0
fi

if $remote; then
    echo "error: --remote is not supported by codex CLI (no remote-control subcommand)." >&2
    echo "hint: closest equivalent is 'codex mcp-server' for MCP stdio serving." >&2
    exit 2
fi

exec codex "${codex_common_args[@]}" "$@"
