# Fork Bomb Investigation

Investigating why Claude Code agents running under ateam occasionally spin at 100%+ CPU with thousands of anonymous unix socket FDs, requiring the process to be killed.

## Observed symptoms

- A single `claude -p --output-format stream-json --verbose --settings ...` process goes to 109% CPU
- 5984+ anonymous unix socket pairs accumulate (lsof shows all `socket unix:` with no peer)
- CHLD signal count stays at 0 (children exit quickly)
- Persists until manually killed (lasted 2m7s in mdtrack run, 14m41s in ateam refactor_small run)
- Coincides with creation of a shell snapshot file in `~/.ateamorg/claude_macos_shared/shell-snapshots/`

## Confirmed dead ends (wrong theories)

- **"11 simultaneous roles → fork bomb"**: ateam caps at 3 concurrent agents; the same-timestamp batch logs are misleading.
- **`codex_core::shell_snapshot` is a Claude Code bug**: The error `ERROR codex_core::shell_snapshot` that appeared in `critical_code_reviewer` stderr was from the **OpenAI Codex CLI** (`codex-cli 0.118.0` at `/opt/homebrew/bin/codex`). That role was running the `codex` agent type, not `claude`. `~/.codex/shell_snapshots/` belongs entirely to the Codex CLI. Irrelevant to Claude Code.
- **`/bin/sh` hardcoded for snapshot validation**: Wrong. Claude Code uses `findSuitableShell()` which reads `$SHELL` (accepts bash or zsh). `/bin/sh` appears only in Windows PowerShell sandboxing code.
- **Snapshot exit-2 → retry loop**: The snapshot is sourced as `source snapshot.sh 2>/dev/null || true` — errors silently ignored. The fact that `/bin/sh snapshot.sh` exits 2 is irrelevant to Claude Code's operation.
- **`go build` caused the spin**: Ruled out by the mdtrack `critic_engineering` run which had no go build, only simple ls/find/git log.

## What we actually understand

### Shell snapshot mechanism (from source: `/Users/nicolas/imports/claude-code/src/utils/bash/ShellSnapshot.ts`)

A snapshot is a **one-time performance cache** of the shell environment. Instead of spawning a new login shell before every bash command, Claude Code:
1. At startup: runs `bash -l -c snapshotScript` to capture shell state (functions, aliases, options, PATH)
2. At each Bash tool invocation: `source snapshot.sh 2>/dev/null || true` to restore that state

Key source details:
- Shell selection: `CLAUDE_CODE_SHELL` env var → `$SHELL` (bash or zsh only) → PATH search
- Snapshot built with login flag (`-l`) and explicit `source ~/.bashrc` inside the script
- Functions captured via `declare -f`, base64-encoded into `eval "$(... | base64 -d)"` blocks
- `SNAPSHOT_CREATION_TIMEOUT` = 10 seconds
- `skipSnapshot: true` option exists in `createBashShellProvider()`

### The contamination problem (fixed)

The snapshot contained **659 base64-encoded completion function definitions** (~589KB, 942 lines) because:
1. Claude Code's `bash -l` sources `.bash_profile` → `.bashrc` → `.bashrc.laptop.Darwin`
2. `.bashrc.laptop.Darwin` line 945-947 sourced `bash_completion.sh` without an interactive guard
3. `bash_completion.sh` checks `$PS1` to detect interactive shell; the user's `.bashrc` sets `PS1` unconditionally so the check passed
4. All of `/opt/homebrew/etc/bash_completion.d/` loaded: golangci-lint, git, brew, bat, etc.

**Fix applied** (in `/Users/nicolas/dotfiles/.bashrc.laptop.Darwin` line 945):
```bash
# before
if [[ -r "/opt/homebrew/etc/profile.d/bash_completion.sh" ]]; then
# after
if [[ $- == *i* ]] && [[ -r "/opt/homebrew/etc/profile.d/bash_completion.sh" ]]; then
```
`$- == *i*` is false in Claude Code's non-interactive `bash -l`. The fix was confirmed to help.

### Env var to force shell

`CLAUDE_CODE_SHELL=/bin/zsh` can be set in a project's `.ateam/runtime.hcl`:
```hcl
agent "claude-ateamorg" {
  base       = "claude"
  config_dir = "~/.ateamorg/claude_macos_shared"
  env = {
    CLAUDE_CODE_SHELL = "/bin/zsh"
  }
}
```
With zsh, bash_completion.d functions (bash-specific) don't load at all.

## Root cause of the spin: still unknown

The socket accumulation mechanism has not been identified. The snapshot validation failure theory is ruled out. Remaining candidates:
- Snapshot **creation** hanging (slow `.bashrc` load → hits 10s timeout → retry loop, each attempt creating a socketpair for Node.js IPC via `execFile`)
- macOS Sonoma `sandbox-exec` deprecation causing repeated subprocess failures with uncleaned IPC sockets
- Something in Claude Code's subagent streaming IPC

Reducing snapshot creation time (659 → 0 completion functions) is the best lever applied so far. Whether this fully prevents the spin is still being observed.

## Files of interest

| Path | Notes |
|------|-------|
| `/Users/nicolas/imports/claude-code/src/utils/bash/ShellSnapshot.ts` | Snapshot creation logic |
| `/Users/nicolas/imports/claude-code/src/utils/shell/bashProvider.ts` | Snapshot sourcing per command |
| `/Users/nicolas/imports/claude-code/src/utils/Shell.ts` | `findSuitableShell()` at line 73 |
| `/Users/nicolas/dotfiles/.bashrc.laptop.Darwin` | Line 945: the fixed completion guard |
| `~/.ateamorg/claude_macos_shared/shell-snapshots/` | Where ateam's claude snapshots live |
| `/Users/nicolas/SyncDatabox/nicmac/projects/ateam/.ateam/runtime.hcl` | Ateam's own agent config |
| `/Users/nicolas/SyncDatabox/nicmac/projects/mdtrack/.ateam/runtime.hcl` | mdtrack agent config (same pattern) |
