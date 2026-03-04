#!/usr/bin/env bash
# Clone the target repo and set up the worktree + directory structure.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/config.sh"

header "Setting up $WORK_DIR"

# Bare clone (skip if already done)
if [[ -d "$WORK_DIR/bare.git" ]]; then
  echo "bare.git already exists, skipping clone"
else
  mkdir -p "$WORK_DIR"
  git clone --bare "$PROJECT_REPO" "$WORK_DIR/bare.git"
fi

# Worktree (skip if already exists)
if [[ -d "$WORK_DIR/code" ]]; then
  echo "code worktree already exists, skipping"
else
  git -C "$WORK_DIR/bare.git" worktree add "$WORK_DIR/code" "$BRANCH"
fi

# Persistent directories
mkdir -p "$WORK_DIR/data" "$WORK_DIR/artifacts" "$WORK_DIR/output"

header "Setup complete"
echo "Bare repo:  $WORK_DIR/bare.git"
echo "Worktree:   $WORK_DIR/code"
echo "Output dir: $WORK_DIR/output"
ls -la "$WORK_DIR"
