#!/usr/bin/env bash
set -euo pipefail

verbose=false

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") [options] [FILE]
       cat FILE | $(basename "$0") [options]

Format Claude Code stream-json logs for human reading.

Options:
  -v, --verbose    Show full tool inputs and text content.
  -h, --help       Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -v|--verbose) verbose=true; shift ;;
        -h|--help) usage; exit 0 ;;
        -*) echo "error: unknown option $1" >&2; exit 1 ;;
        *) break ;;
    esac
done

if [[ $# -gt 0 ]]; then
    [[ -f "$1" ]] || { echo "error: file not found: $1" >&2; exit 1; }
    exec < "$1"
elif [[ -t 0 ]]; then
    usage
    exit 1
fi

command -v jq &>/dev/null || { echo "error: jq is required" >&2; exit 1; }

tool_count=0
text_count=0
event_count=0
turn=0

while IFS= read -r line; do
    event_count=$((event_count + 1))

    etype=$(printf '%s' "$line" | jq -r '.type // empty' 2>/dev/null) || continue
    [[ -z "$etype" ]] && continue

    case "$etype" in
        system)
            subtype=$(printf '%s' "$line" | jq -r '.subtype // empty' 2>/dev/null)
            case "$subtype" in
                init)
                    model=$(printf '%s' "$line" | jq -r '.model // "?"' 2>/dev/null)
                    cwd=$(printf '%s' "$line" | jq -r '.cwd // "?"' 2>/dev/null)
                    sid=$(printf '%s' "$line" | jq -r '.session_id // "?"' 2>/dev/null)
                    version=$(printf '%s' "$line" | jq -r '.claude_code_version // "?"' 2>/dev/null)
                    printf '\033[2m--- session %s  model=%s  v%s\033[0m\n' "$sid" "$model" "$version"
                    printf '\033[2m    cwd: %s\033[0m\n' "$cwd"
                    ;;
                hook_started|hook_response)
                    if $verbose; then
                        hook_name=$(printf '%s' "$line" | jq -r '.hook_name // "?"' 2>/dev/null)
                        printf '\033[2m    hook: %s (%s)\033[0m\n' "$hook_name" "$subtype"
                    fi
                    ;;
                *)
                    [[ -n "$subtype" ]] && printf '\033[2m    system: %s\033[0m\n' "$subtype"
                    ;;
            esac
            ;;

        user)
            turn=$((turn + 1))
            printf '\n\033[1m\033[35m=== Turn %d ===\033[0m\n' "$turn"
            ;;

        assistant)
            # Tool use blocks
            tool_entries=$(printf '%s' "$line" | jq -c '
                [.message.content[]? | select(.type == "tool_use") |
                 { name: .name, input: .input, detail: (
                    if .name == "Bash" then (.input.command // "" | split("\n")[0] | .[0:120])
                    elif .name == "Read" then (.input.file_path // "")
                    elif .name == "Edit" then (.input.file_path // "")
                    elif .name == "Write" then (.input.file_path // "")
                    elif .name == "Glob" then (.input.pattern // "")
                    elif .name == "Grep" then (.input.pattern // "")
                    elif .name == "WebFetch" then (.input.url // "")
                    elif .name == "WebSearch" then (.input.query // "")
                    elif .name == "Agent" then (.input.prompt // "" | .[0:80])
                    elif .name == "ToolSearch" then (.input.query // "")
                    elif .name == "Skill" then (.input.skill_name // "")
                    else null end
                 )}] | .[]
            ' 2>/dev/null) || true

            if [[ -n "$tool_entries" ]]; then
                while IFS= read -r entry; do
                    tool_count=$((tool_count + 1))
                    tname=$(printf '%s' "$entry" | jq -r '.name' 2>/dev/null)

                    if $verbose; then
                        printf '\033[36m  tool #%d: \033[1m%s\033[0m\n' "$tool_count" "$tname"
                        printf '%s' "$entry" | jq -r '.input' 2>/dev/null | sed 's/^/           /'
                    else
                        tdetail=$(printf '%s' "$entry" | jq -r '.detail // empty' 2>/dev/null)
                        if [[ -n "$tdetail" ]]; then
                            [[ ${#tdetail} -gt 100 ]] && tdetail="${tdetail:0:100}..."
                            printf '\033[36m  tool #%d: \033[1m%s\033[0m \033[2m%s\033[0m\n' \
                                "$tool_count" "$tname" "$tdetail"
                        else
                            printf '\033[36m  tool #%d: \033[1m%s\033[0m\n' "$tool_count" "$tname"
                        fi
                    fi
                done <<< "$tool_entries"
            fi

            # Text content
            text_content=$(printf '%s' "$line" | jq -r '
                [.message.content[]? | select(.type == "text") | .text] | first // empty
            ' 2>/dev/null) || true
            if [[ -n "$text_content" ]]; then
                text_count=$((text_count + 1))
                if $verbose; then
                    printf '\033[33m  text #%d:\033[0m\n' "$text_count"
                    printf '%s\n' "$text_content" | sed 's/^/    /'
                else
                    preview=$(printf '%s' "$text_content" | tr '\n' ' ' | sed 's/  */ /g')
                    [[ ${#preview} -gt 120 ]] && preview="${preview:0:120}..."
                    printf '\033[33m  text #%d:\033[0m \033[2m%s\033[0m\n' "$text_count" "$preview"
                fi
            fi

            # Thinking (only in verbose)
            if $verbose; then
                thinking=$(printf '%s' "$line" | jq -r '
                    [.message.content[]? | select(.type == "thinking") | .thinking] | first // empty
                ' 2>/dev/null) || true
                if [[ -n "$thinking" ]]; then
                    printf '\033[2m  thinking:\033[0m\n'
                    printf '%s\n' "$thinking" | sed 's/^/    /'
                fi
            fi

            # If only thinking (no tools, no text), show a compact indicator
            if [[ -z "$tool_entries" && -z "$text_content" ]]; then
                printf '\033[2m  ... thinking\033[0m\n'
            fi
            ;;

        result)
            cost=$(printf '%s' "$line" | jq -r '.total_cost_usd // .cost_usd // "?"' 2>/dev/null)
            [[ "$cost" != "?" ]] && cost=$(printf '%.2f' "$cost")
            duration_ms=$(printf '%s' "$line" | jq -r '.duration_ms // "?"' 2>/dev/null)
            turns=$(printf '%s' "$line" | jq -r '.num_turns // "?"' 2>/dev/null)
            is_err=$(printf '%s' "$line" | jq -r '.is_error // .subtype // "?"' 2>/dev/null)
            in_tok=$(printf '%s' "$line" | jq -r '.usage.input_tokens // "?"' 2>/dev/null)
            out_tok=$(printf '%s' "$line" | jq -r '.usage.output_tokens // "?"' 2>/dev/null)
            cache_read=$(printf '%s' "$line" | jq -r '.usage.cache_read_input_tokens // "?"' 2>/dev/null)

            if [[ "$duration_ms" != "?" && "$duration_ms" =~ ^[0-9]+$ ]]; then
                duration_s=$((duration_ms / 1000))
                duration_human="$((duration_s / 60))m $((duration_s % 60))s"
            else
                duration_human="?"
            fi

            # Tool distribution from accumulated events
            printf '\n\033[1m\033[32m=== Result ===\033[0m\n'
            printf '  Status:    %s\n' "$is_err"
            printf '  Duration:  %s\n' "$duration_human"
            printf '  Cost:      $%s\n' "$cost"
            printf '  Turns:     %s\n' "$turns"
            printf '  Tokens:    in=%s out=%s cache_read=%s\n' "$in_tok" "$out_tok" "$cache_read"
            printf '  Events:    %d (tools=%d, text=%d)\n' "$event_count" "$tool_count" "$text_count"
            ;;
    esac
done

if [[ "$event_count" -gt 0 && "$turn" -eq 0 && "$tool_count" -eq 0 && "$text_count" -eq 0 ]]; then
    printf '\033[2m(%d events processed, nothing to display — incomplete or hooks-only log)\033[0m\n' "$event_count"
fi
