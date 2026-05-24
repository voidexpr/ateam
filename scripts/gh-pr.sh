#!/usr/bin/env bash
set -euo pipefail

# Three-step GitHub PR feedback workflow driven by `ateam exec`.
#
#   status                — verify GH_TOKEN, gh, repo, token scopes.
#   review-feedback PRID  — pre-fetch PR data via gh, then have the
#                           agent triage it into APPLY/ENHANCE/PUSH BACK
#                           at .ateam/shared/gh-pr/PRID/review-feedback.md
#   do-feedback PRID      — agent applies recommendations, replies on
#                           GitHub for push-backs, runs build+tests,
#                           commits per-finding, pushes to the PR branch.
#
# Design: gh API calls are made by THIS script (cheap, deterministic)
# and the results cached under .ateam/shared/gh-pr/PRID/cache/. The
# agent reads files instead of issuing tool calls. The agent still has
# gh available for follow-ups (fetching a file at a SHA, posting
# replies). See the recommendation at the bottom of the script's
# review-feedback prompt.

agent="claude"

usage() {
  cat <<HELP
Usage: $(basename "$0") [--restart] [--push] <subcommand> [PRID]

Subcommands:
  status                Verify GH_TOKEN, gh, repo resolution, and token
                        permissions. No agent invocation, no PRID needed.
  fetch PRID            Resolve the PR's repo and cache its data under
                        .ateam/shared/gh-pr/PRID/cache/. Standalone —
                        useful for debugging repo resolution or token
                        scopes without spending an agent turn.
  review-feedback PRID  Run fetch if needed, then agent triages into
                        .ateam/shared/gh-pr/PRID/review-feedback.md
  do-feedback PRID      Agent applies the report and commits LOCALLY.
                        By default does NOT git push and does NOT post
                        GitHub comments — you review the local commits
                        (and the planned-actions script that gets
                        written) and submit yourself.

Options:
  --push      Have do-feedback also git-push and post GitHub replies
              instead of staging them. Use only after a prior dry
              do-feedback whose output you've inspected.
  --restart   Timestamp-rename existing report(s) for this PRID before
              starting (also forces re-fetch of the cached gh data).
  -h, --help  Show this message and exit.

Resume: review-feedback skips if its report is already non-empty (the
cached gh data is also reused). do-feedback always runs.

Requires:
  - gh on PATH
  - GH_TOKEN env var (fine-grained PAT scoped to the fork repo)
  - git remote pointing at the PR repo
The script forces GH_CONFIG_DIR=.ateam/cache/gh-config so it never
touches ~/.config/gh — see the GH_TOKEN error message for details.
HELP
}

restart=false
push=false
positional=()
while [ $# -gt 0 ]; do
  case "$1" in
    --restart) restart=true; shift ;;
    --push)    push=true; shift ;;
    -h|--help) usage; exit 0 ;;
    --)        shift; while [ $# -gt 0 ]; do positional+=("$1"); shift; done ;;
    -*)        echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
    *)         positional+=("$1"); shift ;;
  esac
done

if [ ${#positional[@]} -lt 1 ]; then
  usage >&2
  exit 2
fi

subcommand="${positional[0]}"
prid="${positional[1]:-}"

case "$subcommand" in
  status) ;;
  fetch|review-feedback|do-feedback)
    if [ -z "$prid" ]; then
      echo "Error: $subcommand requires a PRID" >&2
      usage >&2
      exit 2
    fi
    ;;
  *) echo "Unknown subcommand: $subcommand" >&2; usage >&2; exit 2 ;;
esac

# --- pre-checks: gh + isolated config dir + GH_TOKEN ---

if ! command -v gh >/dev/null 2>&1; then
  echo "Error: gh CLI not found on PATH" >&2
  exit 1
fi

export GH_CONFIG_DIR="$PWD/.ateam/cache/gh-config"
mkdir -p "$GH_CONFIG_DIR"

if [ -z "${GH_TOKEN:-}" ]; then
  cat >&2 <<'MSG'
Error: GH_TOKEN is not set.

Why this script requires GH_TOKEN
---------------------------------
ateam blocks agent access to ~/.config/gh by default so an agent can't
silently use your interactive gh login (which usually has broad scopes
on every repo you can reach). This script uses an isolated gh config
dir (.ateam/cache/gh-config), so it cannot read your interactive auth
and needs a scoped token passed via env.

How to configure GH_TOKEN for fork-PR management
------------------------------------------------
Use a fine-grained personal access token (https://github.com/settings/tokens?type=beta)
restricted to your fork only. Example for a fork YOUR_FORK opened
against FORKED_REPO:

  Resource owner:     <your account or bot account>
  Repository access:  Only select repositories → YOUR_FORK
  Expiration:         7–30 days
  Permissions:
    Contents:         Read and write   (push branches to your fork)
    Pull requests:    Read and write
    Issues:           Read and write   (PR-level comments use the issues API)
    Metadata:         Read-only        (automatic)

You do NOT need access to FORKED_REPO (the upstream) to open the PR
from your fork — the PR object lives on upstream but the branch
lives on YOUR_FORK and only the latter needs write.

Subtleties / limits of a fork-only token:
  - Inline review-comment REPLIES (the /pulls/<n>/comments/<id>/replies
    endpoint) need write on the UPSTREAM repo, not the fork. With a
    fork-only token, this script's do-feedback step falls back to
    PR-level (issue) comments for push-backs.
  - If the PR touches .github/workflows/, push needs the optional
    "Workflows: Read and write" permission too.

Then export the token and re-run:

  export GH_TOKEN=github_pat_xxx

(Don't echo $GH_TOKEN in shared logs, and don't commit
.ateam/cache/gh-config — gitignore that path if it isn't already.)

For maximum safety: use a dedicated bot account, fork under that
account, and grant the token only to that single fork repo.
MSG
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "Error: 'gh auth status' failed despite GH_TOKEN being set." >&2
  echo "       Token may be invalid, expired, or missing scopes." >&2
  echo "       Run: gh auth status" >&2
  exit 1
fi

gh_user="$(gh api user --jq .login 2>/dev/null || echo unknown)"

# --- helpers ---

step() {
  printf '\n\033[1;36m── %s ──\033[0m\n' "$1"
}

backup() {
  [ -f "$1" ] || return 0
  cp -p "$1" "${1%.md}.$(date +%Y-%m-%d_%H%M%S).md"
  rm -f "$1"
}

# Resolve which repo the PR lives on. For fork-PR workflows the PR is
# typically on the upstream, so try current repo first and fall back to
# the parent. Echoes "OWNER/REPO" or returns non-zero.
resolve_pr_repo() {
  local prid="$1"
  local current parent
  current=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)
  parent=$(gh repo view --json parent -q '.parent.nameWithOwner // empty' 2>/dev/null || true)

  if [ -n "$current" ] && gh pr view "$prid" --repo "$current" --json number >/dev/null 2>&1; then
    echo "$current"
    return 0
  fi
  if [ -n "$parent" ] && gh pr view "$prid" --repo "$parent" --json number >/dev/null 2>&1; then
    echo "$parent"
    return 0
  fi
  return 1
}

# Pre-fetch PR data into cache_dir as flat JSON files the agent will read.
prefetch_pr() {
  local prid="$1" repo="$2" cache_dir="$3"
  mkdir -p "$cache_dir"
  # Note: gh has no `baseRepository` field (only headRepository). The base
  # side is on the current repo by definition; for the head we need the
  # explicit field since the head branch may live on a fork.
  gh pr view "$prid" --repo "$repo" \
    --json number,title,body,state,headRefName,baseRefName,url,isDraft,author,headRepository,headRepositoryOwner,additions,deletions,changedFiles \
    > "$cache_dir/pr.json"
  gh pr diff "$prid" --repo "$repo" > "$cache_dir/diff.patch"
  gh api -X GET "repos/$repo/pulls/$prid/reviews"  --paginate > "$cache_dir/reviews.json"
  gh api -X GET "repos/$repo/pulls/$prid/comments" --paginate > "$cache_dir/inline-comments.json"
  gh api -X GET "repos/$repo/issues/$prid/comments" --paginate > "$cache_dir/pr-comments.json"
  gh pr view "$prid" --repo "$repo" --json files -q '.files' > "$cache_dir/files.json"

  # gh sometimes prints "Unknown JSON field" errors but exits 0, leaving
  # an empty file. Defensive check so the agent never reads a stub.
  for required in pr.json diff.patch reviews.json inline-comments.json pr-comments.json files.json; do
    if [ ! -s "$cache_dir/$required" ]; then
      # pr-comments and reviews can legitimately be empty arrays "[]";
      # accept those, reject literal zero-byte files.
      case "$required" in
        reviews.json|pr-comments.json|inline-comments.json|files.json)
          # Write an empty JSON array so downstream jq calls don't fail.
          if [ ! -e "$cache_dir/$required" ]; then
            echo "[]" > "$cache_dir/$required"
          elif [ ! -s "$cache_dir/$required" ]; then
            echo "[]" > "$cache_dir/$required"
          fi
          ;;
        *)
          echo "Error: prefetch produced empty $cache_dir/$required" >&2
          echo "       The preceding gh command likely failed silently." >&2
          return 1
          ;;
      esac
    fi
  done
}

# Ensure the cache is populated. Returns the resolved pr_repo via stdout.
# Honors $restart (forces re-fetch). Used by fetch, review-feedback, and
# do-feedback so any of them can run standalone.
ensure_fetch() {
  local prid="$1" cache_dir="$2"
  local pr_repo
  if ! pr_repo=$(resolve_pr_repo "$prid"); then
    echo "Error: PR #$prid not found on the current repo or its parent." >&2
    echo "       Check with: gh pr view $prid" >&2
    return 1
  fi
  if [ "$restart" = true ]; then
    rm -rf "$cache_dir"
  fi
  if [ -f "$cache_dir/pr.json" ]; then
    step "PR data cached → $cache_dir/ (use --restart to refetch)" >&2
  else
    step "fetch PR #$prid from $pr_repo → $cache_dir/" >&2
    prefetch_pr "$prid" "$pr_repo" "$cache_dir"
  fi
  echo "$pr_repo"
}

# --- subcommands ---

run_status() {
  echo "GitHub:    $gh_user"
  echo "gh config: $GH_CONFIG_DIR"
  echo ""

  local current parent
  current=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo "<not in a gh-resolvable repo>")
  parent=$(gh repo view --json parent -q '.parent.nameWithOwner // empty' 2>/dev/null || true)
  echo "Repos:"
  echo "  current:  $current"
  if [ -n "$parent" ]; then
    echo "  parent:   $parent (current is a fork)"
  else
    echo "  parent:   (current is not a fork)"
  fi
  echo ""

  echo "Token permissions (push = write on Contents):"
  for repo in "$current" "$parent"; do
    [ -z "$repo" ] || [ "$repo" = "<not in a gh-resolvable repo>" ] && continue
    local perms
    perms=$(gh api "repos/$repo" --jq '.permissions | "admin=\(.admin) push=\(.push) pull=\(.pull)"' 2>/dev/null || echo "ERROR (no access?)")
    echo "  $repo: $perms"
  done
  echo ""

  echo "Tip: PR-level comments work with fork-only tokens."
  echo "     Inline-reply on UPSTREAM PRs requires push on the upstream repo."
}

run_fetch() {
  local base_dir cache_dir pr_repo
  base_dir=".ateam/shared/gh-pr/$prid"
  cache_dir="$base_dir/cache"
  mkdir -p "$base_dir"

  pr_repo=$(ensure_fetch "$prid" "$cache_dir") || exit 1

  echo ""
  echo "PR repo:   $pr_repo"
  echo "Cache:     $cache_dir"
  echo ""
  echo "Files:"
  for f in pr.json diff.patch reviews.json inline-comments.json pr-comments.json files.json; do
    if [ -f "$cache_dir/$f" ]; then
      local sz cnt
      sz=$(wc -c < "$cache_dir/$f" | tr -d ' ')
      case "$f" in
        *.json)
          cnt=$(jq 'if type=="array" then length else 1 end' "$cache_dir/$f" 2>/dev/null || echo "?")
          printf "  %-22s %8s bytes  %s items\n" "$f" "$sz" "$cnt"
          ;;
        *)
          printf "  %-22s %8s bytes\n" "$f" "$sz"
          ;;
      esac
    fi
  done
}

run_review() {
  local pr_repo cache_dir base_dir review_out role
  base_dir=".ateam/shared/gh-pr/$prid"
  cache_dir="$base_dir/cache"
  review_out="$base_dir/review-feedback.md"
  mkdir -p "$base_dir"

  if [ "$restart" = true ]; then
    backup "$review_out"
  fi

  if [ -s "$review_out" ]; then
    step "review-feedback PR #$prid — cached ($review_out), skipping"
    return 0
  fi

  pr_repo=$(ensure_fetch "$prid" "$cache_dir") || exit 1
  role=$(basename "$pr_repo")

  step "review-feedback PR #$prid by $agent (role=$role)"
  ateam exec --agent "$agent" --action "gh-pr/review-feedback" --role "$role" <<EOF
You are triaging review feedback on GitHub PR #$prid in repo $pr_repo.

ALL PR data is already fetched. Read these files — do not re-run gh
for any of them:
  $cache_dir/pr.json              PR metadata
  $cache_dir/diff.patch           the unified diff
  $cache_dir/reviews.json         review summaries (state: APPROVED / CHANGES_REQUESTED / COMMENTED)
  $cache_dir/inline-comments.json review comments tied to file:line
  $cache_dir/pr-comments.json     PR-level (issue API) comments
  $cache_dir/files.json           list of changed files with line counts

You MAY use gh for follow-ups only when the cache is insufficient:
  - fetching a specific file at a SHA: gh api repos/$pr_repo/contents/PATH?ref=SHA
  - resolving a comment thread that references another PR / issue
Do NOT re-fetch anything already present in cache/.

Skip resolved threads and comments authored by the PR author.

Write $review_out. For each piece of remaining feedback, emit one section:
  ### <short title> — <reviewer login> [file:line if inline]
  > <exact quote of the comment>

  **Recommendation:** APPLY | ENHANCE | PUSH BACK

  **Reasoning:** one to three sentences. For APPLY, sketch the change.
  For ENHANCE, name the original ask + what to add beyond it.
  For PUSH BACK, give a clearly-argued counter grounded in the code or
  prior decisions — not a vague "I disagree".

End with a one-paragraph summary: counts of APPLY / ENHANCE / PUSH BACK
and any cross-cutting themes.
EOF
}

run_do() {
  local pr_repo cache_dir base_dir review_out do_log submit_script
  base_dir=".ateam/shared/gh-pr/$prid"
  cache_dir="$base_dir/cache"
  review_out="$base_dir/review-feedback.md"
  do_log="$base_dir/do-feedback.md"
  submit_script="$base_dir/submit.sh"

  if [ ! -s "$review_out" ]; then
    echo "Error: $review_out is missing or empty." >&2
    echo "  Run: $(basename "$0") review-feedback $prid" >&2
    exit 1
  fi

  pr_repo=$(ensure_fetch "$prid" "$cache_dir") || exit 1

  local role pr_url pr_head head_before
  role=$(basename "$pr_repo")
  pr_url=$(jq -r '.url' "$cache_dir/pr.json" 2>/dev/null || echo "")
  pr_head=$(jq -r '.headRefName' "$cache_dir/pr.json" 2>/dev/null || echo "")
  head_before=$(git rev-parse HEAD 2>/dev/null || echo "")

  local mode_line
  if [ "$push" = true ]; then
    mode_line="MODE: --push given. After committing, run git push and execute the planned gh actions yourself."
  else
    mode_line="MODE: stage only. Do NOT git push. Do NOT post any gh comments / replies. Instead, write the exact gh command line for each planned reply into $submit_script."
  fi

  step "do-feedback PR #$prid by $agent (role=$role, push=$push)"
  ateam exec --agent "$agent" --action "gh-pr/do-feedback" --role "$role" <<EOF
You are applying review feedback to GitHub PR #$prid in repo $pr_repo.
The recommendation report is at $review_out — treat it as the plan.

Pre-fetched data is available at $cache_dir/ (same layout as for
review-feedback); read from there instead of re-running gh.

$mode_line

For each section in the report:
  - APPLY:     make the change in code.
  - ENHANCE:   apply the original suggestion AND the enhancement.
  - PUSH BACK: do NOT change code. Plan a GitHub reply. In-thread reply:
                 gh api -X POST \\
                   repos/$pr_repo/pulls/$prid/comments/{COMMENT_ID}/replies \\
                   -f body="..."
               (the comment IDs are in $cache_dir/inline-comments.json).
               PR-level comment fallback (use when there's no thread or
               when a fork-only token would 403 on the upstream reply):
                 gh pr comment $prid --repo $pr_repo -b "..."

In stage-only mode (default), $submit_script must be a self-contained
bash script starting with "#!/usr/bin/env bash\\nset -euo pipefail" and
containing exactly the commands the user should run to publish your
work:
  1. git push (to the PR head branch — verify branch matches $pr_head)
  2. One gh-command block per push-back, with a brief comment line
     above each saying which finding it's for.
Make it idempotent where you can.

After the code changes (regardless of mode):
  - Build and test using this project's conventional commands (check
    README, CLAUDE.md/AGENTS.md, Makefile). Record exact commands.
  - Commit per-finding when reasonable; group same-file changes.

Write $do_log (overwriting OK) with:
  1. Applied   — finding → commit SHA + file(s).
  2. Enhanced  — finding → what was added beyond the original ask.
  3. Pushed back (planned) — finding → reply target + body text.
  4. Tests     — exact commands run and pass/fail counts.

End with a one-line summary: counts + whether anything was pushed.
EOF

  step "do-feedback complete"
  echo "PR:          ${pr_url:-(unknown)}"
  echo "PR branch:   ${pr_head:-(unknown)} (your local branch)"
  if [ -n "$head_before" ]; then
    local head_after new_commits
    head_after=$(git rev-parse HEAD 2>/dev/null || echo "")
    if [ -n "$head_after" ] && [ "$head_after" != "$head_before" ]; then
      new_commits=$(git rev-list --count "$head_before..$head_after" 2>/dev/null || echo "?")
      echo "New commits: $new_commits ($head_before..$head_after)"
      echo "Review with: git log --stat $head_before..$head_after"
    else
      echo "New commits: 0 (no changes applied locally)"
    fi
  fi
  echo "Report:      $do_log"
  if [ "$push" = false ]; then
    echo ""
    echo "Nothing has been pushed and no GitHub comments have been posted."
    if [ -s "$submit_script" ]; then
      echo "To publish, review then run:"
      echo "  less $submit_script"
      echo "  bash $submit_script"
    else
      echo "Note: agent did not write $submit_script — inspect $do_log for what it intended."
    fi
  fi
}

case "$subcommand" in
  status)          run_status ;;
  fetch)           run_fetch ;;
  review-feedback) run_review ;;
  do-feedback)     run_do ;;
esac

step "done"
