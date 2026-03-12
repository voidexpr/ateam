#!/usr/bin/env bash
set -euo pipefail

SETTINGS_FILE="/tmp/claude-sandbox.settings.$(date +%Y-%m-%d_%H_%M_%S).$$.json"

web_fetch=true
always_approve=false
remote=false
one_shot=false
sandbox_verbose=false
extra_dirs=()

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") [wrapper-options] [claude-options...]

Wrapper options:
  --no-web-fetch      Disable WebFetch tool.
  --always-approve    Bypass all permission prompts.
  --remote            Launch in remote-control mode.
  --one-shot          Run non-interactively with stream-json monitoring.
  --sandbox-verbose   Verbose live event output in --one-shot mode.
  --path <dir>        Add extra writable directory (repeatable).
  --help              Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --help|-h)
            usage
            exit 0
            ;;
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
        *)
            break
            ;;
    esac
done

# Build extra path JSON fragments (each prefixed with comma for appending)
extra_path_json=""
if [[ ${#extra_dirs[@]} -gt 0 ]]; then
    for d in "${extra_dirs[@]}"; do
        extra_path_json+=","$'\n'"        \"$d\""
    done
fi

cat > "$SETTINGS_FILE" <<EOF
{
  "permissions": {
    "defaultMode": "acceptEdits",
    "additionalDirectories": [
      "~/Library/Caches"${extra_path_json}
    ],
    "allow": [
      "Read",
      "Edit",
      "Write",
      "Glob",
      "Grep",
      "Bash(*)",
      "Agent",
      "NotebookEdit",
      "AskUserQuestion",
      "Skill",
      "EnterPlanMode",
      "ExitPlanMode",
      "EnterWorktree",
      "LSP",
      "SendMessage",
      "TaskCreate",
      "TaskGet",
      "TaskList",
      "TaskOutput",
      "TaskStop",
      "TaskUpdate",
      "TeamCreate",
      "TeamDelete",
      "WebSearch"$($web_fetch && echo ',
      "WebFetch"')
    ],
    "deny": [
      "Bash(ateam:init)",
      "Bash(ateam:install)"
    ]
  },
  "sandbox": {
    "enabled": true,
    "autoAllowBashIfSandboxed": true,
    "allowUnsandboxedCommands": false,
    "excludedCommands": [
      "git:*",
      "go:*",
      "cargo:*",
      "npm:*", "npx:*",
      "bun:*", "bunx:*",
      "pnpm:*",
      "yarn:*",
      "pip:*", "pip3:*", "uv:*", "poetry:*",
      "gradle:*", "mvn:*",
      "docker:*",
      "ateam:*"
    ],
    "filesystem": {
      "allowWrite": [
        "~/Library/Caches"${extra_path_json}
      ]
    },
    "network": {
      "allowedDomains": [
        "*.github.com",
        "*.githubusercontent.com",
        "registry.npmjs.org",
        "api.anthropic.com"
      ],
      "allowLocalBinding": true
    }
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
echo "Custom Claude Sandbox Settings: $SETTINGS_FILE" >&2
cat "$SETTINGS_FILE" >&2
echo "claude specific args passed through: $*" >&2
echo >&2

extra_flags=()
$always_approve && extra_flags+=(--dangerously-skip-permissions)

if $one_shot; then
    if [[ $# -eq 0 ]]; then
        if [[ -t 0 ]]; then
            echo "error: --one-shot requires a prompt argument or stdin" >&2
            exit 1
        fi
        set -- "$(cat)"
    fi
    command -v jq &>/dev/null || { echo "error: jq is required for --one-shot" >&2; exit 1; }

    STREAM_FILE="$(mktemp "${TMPDIR:-/tmp}/claude-oneshot-XXXXXX.jsonl")"
    STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/claude-oneshot-XXXXXX.log")"
    cleanup() { rm -f "$STREAM_FILE" "$STDERR_FILE"; }
    trap cleanup EXIT

    # Live monitor: tail the stream and print progress lines to stderr
    live_monitor() {
        local start_ts=$SECONDS tool_count=0 event_count=0 text_count=0

        while [[ ! -f "$STREAM_FILE" ]]; do sleep 0.3; done

        # Use tail -n +1 -f to read from beginning, avoiding race with initial writes
        tail -n +1 -f "$STREAM_FILE" 2>/dev/null | while IFS= read -r line; do
            event_count=$((event_count + 1))
            local elapsed=$(( SECONDS - start_ts ))
            local ts
            ts=$(printf '%02d:%02d' $((elapsed / 60)) $((elapsed % 60)))

            local etype
            etype=$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null) || continue

            case "$etype" in
                system)
                    local subtype
                    subtype=$(printf '%s' "$line" | jq -r '.subtype // empty' 2>/dev/null)
                    case "$subtype" in
                        init)
                            local sid model
                            sid=$(printf '%s' "$line" | jq -r '.session_id // "?"' 2>/dev/null)
                            model=$(printf '%s' "$line" | jq -r '.model // empty' 2>/dev/null)
                            printf '\033[2m[%s] session=%s model=%s\033[0m\n' "$ts" "$sid" "$model" >&2
                            ;;
                        *)
                            [[ -n "$subtype" ]] && printf '\033[2m[%s] system: %s\033[0m\n' "$ts" "$subtype" >&2
                            ;;
                    esac
                    ;;
                assistant)
                    # Extract tool_use blocks with full input in verbose, key detail otherwise
                    local tool_entries
                    if $sandbox_verbose; then
                        tool_entries=$(printf '%s' "$line" | jq -c '
                            [.message.content[]? | select(.type == "tool_use") |
                             { name: .name, input: .input }] | .[]
                        ' 2>/dev/null) || true
                    else
                        tool_entries=$(printf '%s' "$line" | jq -c '
                            [.message.content[]? | select(.type == "tool_use") |
                             { name: .name, detail: (
                                if .name == "Bash" then (.input.command // "" | split("\n")[0] | .[0:80])
                                elif .name == "Read" then (.input.file_path // "")
                                elif .name == "Edit" then (.input.file_path // "")
                                elif .name == "Write" then (.input.file_path // "")
                                elif .name == "Glob" then (.input.pattern // "")
                                elif .name == "Grep" then (.input.pattern // "")
                                elif .name == "WebFetch" then (.input.url // "")
                                elif .name == "WebSearch" then (.input.query // "")
                                elif .name == "Agent" then (.input.prompt // "" | .[0:60])
                                else null end
                             )}] | .[]
                        ' 2>/dev/null) || true
                    fi

                    if [[ -n "$tool_entries" ]]; then
                        while IFS= read -r entry; do
                            tool_count=$((tool_count + 1))
                            local tname
                            tname=$(printf '%s' "$entry" | jq -r '.name' 2>/dev/null)

                            if $sandbox_verbose; then
                                printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m\n' \
                                    "$ts" "$tool_count" "$tname" >&2
                                printf '%s' "$entry" | jq -r '.input' 2>/dev/null | sed 's/^/             /' >&2
                            else
                                local tdetail
                                tdetail=$(printf '%s' "$entry" | jq -r '.detail // empty' 2>/dev/null)
                                if [[ -n "$tdetail" ]]; then
                                    [[ ${#tdetail} -gt 70 ]] && tdetail="${tdetail:0:70}..."
                                    printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m \033[2m%s\033[0m\n' \
                                        "$ts" "$tool_count" "$tname" "$tdetail" >&2
                                else
                                    printf '\033[36m[%s]\033[0m tool #%d: \033[1m%s\033[0m\n' \
                                        "$ts" "$tool_count" "$tname" >&2
                                fi
                            fi
                        done <<< "$tool_entries"
                    fi

                    # Show text content (thinking / response text)
                    local text_content
                    text_content=$(printf '%s' "$line" | jq -r '
                        [.message.content[]? | select(.type == "text") | .text] | first // empty
                    ' 2>/dev/null) || true
                    if [[ -n "$text_content" ]]; then
                        text_count=$((text_count + 1))
                        printf '\033[33m[%s]\033[0m \033[2mtext #%d:\033[0m' "$ts" "$text_count" >&2
                        if $sandbox_verbose; then
                            printf '\n' >&2
                            printf '%s\n' "$text_content" | sed 's/^/             /' >&2
                        else
                            local preview
                            preview=$(printf '%s' "$text_content" | tr '\n' ' ' | sed 's/  */ /g')
                            [[ ${#preview} -gt 80 ]] && preview="${preview:0:80}..."
                            printf ' \033[2m%s\033[0m\n' "$preview" >&2
                        fi
                    fi

                    if [[ -z "$tool_entries" && -z "$text_content" ]]; then
                        printf '\033[2m[%s] events=%d tools=%d thinking...\033[0m\n' "$ts" "$event_count" "$tool_count" >&2
                    fi
                    ;;
                result)
                    local cost duration turns is_err in_tok out_tok
                    cost=$(printf '%s' "$line" | jq -r '.total_cost_usd // .cost_usd // "?"' 2>/dev/null)
                    [[ "$cost" != "?" ]] && cost=$(printf '%.2f' "$cost")
                    duration=$(printf '%s' "$line" | jq -r '.duration_ms // "?"' 2>/dev/null)
                    turns=$(printf '%s' "$line" | jq -r '.num_turns // "?"' 2>/dev/null)
                    is_err=$(printf '%s' "$line" | jq -r '.is_error // false' 2>/dev/null)
                    in_tok=$(printf '%s' "$line" | jq -r '.usage.input_tokens // "?"' 2>/dev/null)
                    out_tok=$(printf '%s' "$line" | jq -r '.usage.output_tokens // "?"' 2>/dev/null)
                    printf '\033[32m[%s] done\033[0m cost=$%s turns=%s tokens=%s/%s duration=%sms error=%s\n' \
                        "$ts" "$cost" "$turns" "$in_tok" "$out_tok" "$duration" "$is_err" >&2
                    break
                    ;;
            esac
        done
    }

    live_monitor &
    MONITOR_PID=$!

    claude --settings "$SETTINGS_FILE" "${extra_flags[@]}" \
        -p --output-format stream-json --verbose \
        "$@" \
        > "$STREAM_FILE" 2>"$STDERR_FILE" || true

    sleep 0.5
    kill "$MONITOR_PID" 2>/dev/null; wait "$MONITOR_PID" 2>/dev/null || true

    # --- Summary ---
    RESULT_JSON="$(jq -s '[.[] | select(.type == "result")] | last // {}' "$STREAM_FILE" 2>/dev/null)"
    COST_RAW="$(printf '%s' "$RESULT_JSON" | jq -r '.total_cost_usd // .cost_usd // "?"')"
    [[ "$COST_RAW" != "?" ]] && COST_RAW="$(printf '%.2f' "$COST_RAW")"
    DURATION_MS="$(printf '%s' "$RESULT_JSON" | jq -r '.duration_ms // "?"')"
    NUM_TURNS="$(printf '%s' "$RESULT_JSON" | jq -r '.num_turns // "?"')"
    IS_ERROR="$(printf '%s' "$RESULT_JSON" | jq -r '.is_error // false')"

    if [[ "$DURATION_MS" != "?" ]]; then
        DURATION_S=$(( DURATION_MS / 1000 ))
        DURATION_HUMAN="$(( DURATION_S / 60 ))m $(( DURATION_S % 60 ))s"
    else
        DURATION_HUMAN="?"
    fi

    INPUT_TOKENS="$(printf '%s' "$RESULT_JSON" | jq -r '.usage.input_tokens // "?"')"
    OUTPUT_TOKENS="$(printf '%s' "$RESULT_JSON" | jq -r '.usage.output_tokens // "?"')"
    CACHE_READ="$(printf '%s' "$RESULT_JSON" | jq -r '.usage.cache_read_input_tokens // "?"')"

    TOOL_DIST="$(jq -r 'select(.type == "assistant") | .message.content[]? | select(.type == "tool_use") | .name' "$STREAM_FILE" 2>/dev/null | sort | uniq -c | sort -rn)"

    AGENT_RESPONSE="$(jq -s '
        [.[] | select(.type == "assistant") | .message.content[]?
         | select(.type == "text") | .text] | last // empty
    ' "$STREAM_FILE" 2>/dev/null)"

    cat >&2 <<EOF

=== Run Summary ===

Exit code:  $IS_ERROR
Duration:   $DURATION_HUMAN
Cost:       \$$COST_RAW
Turns:      $NUM_TURNS
Tokens:     input=$INPUT_TOKENS output=$OUTPUT_TOKENS cache_read=$CACHE_READ

Tools used:
$TOOL_DIST
EOF

    if [[ -n "$AGENT_RESPONSE" && "$AGENT_RESPONSE" != "null" ]]; then
        echo "" >&2
        echo "=== Agent Response ===" >&2
        echo "" >&2
        printf '%s' "$AGENT_RESPONSE" | jq -r '.' >&2
    fi

    exit 0
fi

if $remote; then
    exec claude --settings "$SETTINGS_FILE" "${extra_flags[@]}" remote-control "$@"
else
    exec claude --settings "$SETTINGS_FILE" "${extra_flags[@]}" "$@"
fi
