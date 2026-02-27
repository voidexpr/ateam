# ATeam Proof of Concept

Validate the core Claude Code interaction model before building the Go CLI.
Everything here uses raw commands — no framework code, just Docker, git, and bash.

## What We're Validating

1. **The agent loop works** — `claude -p` with stream-json, file-based prompt, inside Docker, produces useful output
2. **The report/action cycle works** — audit produces a report, a second invocation acts on it
3. **Persistent workspace pays off** — second run is faster because node_modules/data survive
4. **Stream-json is parseable** — we can extract cost and usage data for budget tracking
5. **Coordinator pattern works** — a Claude Code session can read reports and make triage decisions

What we're NOT testing (no risk, standard engineering): SQLite operations, git worktree management, Docker lifecycle, config.toml parsing.

---

## Prerequisites

- Docker installed and running
- `ANTHROPIC_API_KEY` set in your environment
- A real project git repo with some code and tests (Node/Python/Go — anything with a test runner)

---

## Step 0: Setup

Pick a real project. Adjust `PROJECT_REPO` to your repo.

```bash
PROJECT_REPO="https://github.com/you/yourproject.git"
WORK_DIR="$HOME/ateam-validation"
mkdir -p "$WORK_DIR"

# Clone bare + create a worktree (simulating what ateam init does)
git clone --bare "$PROJECT_REPO" "$WORK_DIR/bare.git"
git -C "$WORK_DIR/bare.git" worktree add "$WORK_DIR/code" main

# Create persistent data and output dirs
mkdir -p "$WORK_DIR/data" "$WORK_DIR/artifacts" "$WORK_DIR/output"
```

---

## Step 1: Build a Fat Container

Validates the Dockerfile pattern from the spec (§7.2–7.3). Adjust packages for your project's stack.

```bash
cat > "$WORK_DIR/Dockerfile" << 'EOF'
FROM node:20-bookworm-slim

# System tools + Claude Code
RUN apt-get update && apt-get install -y \
    git curl sudo ca-certificates \
    ripgrep fd-find jq tree \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g @anthropic-ai/claude-code

# Non-root user
ARG USERNAME=node
RUN mkdir -p /data /artifacts /output /agent-data \
    && chown -R $USERNAME:$USERNAME /data /artifacts /output /agent-data

USER $USERNAME
WORKDIR /workspace
EOF

docker build -t ateam-test "$WORK_DIR"
```

**What to check:**
- Image builds successfully
- Claude Code is available: `docker run --rm ateam-test claude --version`

---

## Step 2: Write the Audit Prompt

The testing agent's audit mode prompt. This is what the prompt builder would assemble from role.md + knowledge + instructions.

```bash
cat > "$WORK_DIR/prompt-audit.md" << 'PROMPT'
# Role: Testing Agent — Audit Mode

You are a testing specialist. Your job is to analyze this codebase
and produce a report on testing gaps.

## What To Do

1. Explore the project structure. Understand what it does.
2. Look at existing tests. Run them if a test command exists.
3. Identify the most important untested code paths.
4. For each gap, describe what test would cover it and why it matters.

## What NOT To Do

- Do not write or modify any code in this mode.
- Do not install new dependencies.
- Do not over-report. Focus on the 3-5 most impactful gaps.

## Output

Write your report to /output/report.md with this structure:

```markdown
# Testing Audit Report

## Summary
(2-3 sentences: overall test health)

## Findings

### 1. (title)
- **File(s):** ...
- **Risk:** high/medium/low
- **What's missing:** ...
- **Suggested test:** ...

(repeat for each finding)
```

Be concise. Be specific. Reference actual file paths and function names.
PROMPT
```

---

## Step 3: Run the Audit Agent

The core validation — does the full chain work?

```bash
docker run --rm \
  --name ateam-test-audit \
  --cpus=2 --memory=4g \
  -v "$WORK_DIR/code:/workspace:rw" \
  -v "$WORK_DIR/data:/data:rw" \
  -v "$WORK_DIR/artifacts:/artifacts:rw" \
  -v "$WORK_DIR/output:/output:rw" \
  -v "$WORK_DIR/prompt-audit.md:/agent-data/prompt.md:ro" \
  -e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY" \
  ateam-test \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --max-budget-usd 2.00 \
      2>/output/stderr.log \
      | tee /output/stream.jsonl
    echo $? > /output/exit_code
  '
```

**What to check:**

```bash
# Did it exit cleanly?
cat "$WORK_DIR/output/exit_code"

# Did it produce a report?
cat "$WORK_DIR/output/report.md"

# Is stream-json parseable? Can we extract cost?
head -5 "$WORK_DIR/output/stream.jsonl"
jq -s '[.[] | select(.type == "result")] | last' \
  "$WORK_DIR/output/stream.jsonl"

# Any errors?
cat "$WORK_DIR/output/stderr.log"
```

**Questions to answer:**
- Is the report useful? Does it reference real files and functions?
- Is `report.md` in the expected format, or did the agent deviate?
- Does stream-json contain cost/token data we can parse?

---

## Step 4: Run the Implement Agent

Validates the audit → implement cycle. The implement prompt includes the audit report.

### Write the implement prompt

```bash
cat > "$WORK_DIR/prompt-implement.md" << PROMPT
# Role: Testing Agent — Implement Mode

You are a testing specialist. Implement the tests recommended in the
audit report below.

## Audit Report

$(cat "$WORK_DIR/output/report.md")

## Instructions

1. Implement the suggested tests, starting with highest-risk.
2. Run the test suite after each change.
3. Stop after the top 3 findings.
4. Write a summary to /output/actions.md listing what you changed,
   test results, and anything you skipped.

Do not refactor production code. Only add or modify test files.
PROMPT
```

Note: the heredoc uses `PROMPT` without quotes so `$(cat ...)` expands and inlines the actual report content. This is how the prompt builder would work — assembling everything into one file.

### Run it

```bash
# Clean stream output from previous run (keep report.md)
rm -f "$WORK_DIR/output/stream.jsonl" "$WORK_DIR/output/exit_code" "$WORK_DIR/output/stderr.log"

docker run --rm \
  --name ateam-test-implement \
  --cpus=2 --memory=4g \
  -v "$WORK_DIR/code:/workspace:rw" \
  -v "$WORK_DIR/data:/data:rw" \
  -v "$WORK_DIR/artifacts:/artifacts:rw" \
  -v "$WORK_DIR/output:/output:rw" \
  -v "$WORK_DIR/prompt-implement.md:/agent-data/prompt.md:ro" \
  -e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY" \
  ateam-test \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --max-budget-usd 3.00 \
      2>/output/stderr.log \
      | tee /output/stream.jsonl
    echo $? > /output/exit_code
  '
```

**What to check:**

```bash
# Did it produce an actions summary?
cat "$WORK_DIR/output/actions.md"

# What changed in the code?
cd "$WORK_DIR/code" && git diff --stat

# Do the tests actually pass?
cd "$WORK_DIR/code" && npm test   # or your test command
```

**Questions to answer:**
- Did the implement agent follow the report's recommendations?
- Are the new tests real and useful, or boilerplate?
- Did it stay within scope (test files only, no production code changes)?

---

## Step 5: Validate Persistent Workspace

Reset the worktree and run the audit again. This proves that persistent
data directories (node_modules, build caches) make subsequent runs faster.

```bash
# Reset tracked files to latest, preserving untracked (node_modules, etc.)
cd "$WORK_DIR/code" && git checkout -- .

# Clean output
rm -f "$WORK_DIR/output/stream.jsonl" "$WORK_DIR/output/exit_code"

# Time the second audit run
time docker run --rm \
  --name ateam-test-audit-2 \
  --cpus=2 --memory=4g \
  -v "$WORK_DIR/code:/workspace:rw" \
  -v "$WORK_DIR/data:/data:rw" \
  -v "$WORK_DIR/output:/output:rw" \
  -v "$WORK_DIR/prompt-audit.md:/agent-data/prompt.md:ro" \
  -e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY" \
  ateam-test \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --max-budget-usd 2.00 \
      2>/output/stderr.log \
      | tee /output/stream.jsonl
    echo $? > /output/exit_code
  '
```

**What to check:**
- Compare wall-clock time with Step 3
- Did the agent skip `npm install` (or equivalent) because node_modules exists?
- Is the second report different (it should reflect the tests added in Step 4)?

---

## Step 6: Validate Stream-JSON Parsing

After the runs above, check whether we can reliably extract cost and usage data
for budget tracking.

```bash
# What event types are in the stream?
jq -r '.type' "$WORK_DIR/output/stream.jsonl" | sort | uniq -c | sort -rn

# Extract the result event (should contain cost/usage)
jq -s '[.[] | select(.type == "result")] | last' \
  "$WORK_DIR/output/stream.jsonl"

# Try to get cost specifically
jq -s '[.[] | select(.type == "result")] | last | .cost_usd // .usage // "NOT FOUND"' \
  "$WORK_DIR/output/stream.jsonl"

# Count tool-use events (how many actions did the agent take?)
jq -s '[.[] | select(.type == "tool_use")] | length' \
  "$WORK_DIR/output/stream.jsonl"
```

**Questions to answer:**
- Is cost data available in stream-json? What field name?
- Can we compute total tokens from usage events?
- If cost isn't in stream-json, what's our fallback for budget tracking?

---

## Step 7: Validate the Coordinator Pattern

Can a Claude Code session read a report and make reasonable triage decisions?

### Write the coordinator prompt

```bash
cat > "$WORK_DIR/prompt-coordinator.md" << PROMPT
You are the ATeam coordinator. Review the following agent output
and make decisions.

## Testing Agent — Audit Report

$(cat "$WORK_DIR/output/report.md")

## Testing Agent — Implementation Summary

$(cat "$WORK_DIR/output/actions.md")

## Your Task

For each finding in the audit report, decide:
- **done**: already implemented in the actions summary
- **defer**: not worth doing now (explain briefly)
- **ask**: need human input (explain what's unclear)

Then assess overall: is the project in better shape? What should
the testing agent focus on next time?

Write your decisions to /output/decisions.md.
PROMPT
```

### Run it

```bash
rm -f "$WORK_DIR/output/coordinator-stream.jsonl"

docker run --rm \
  --name ateam-test-coordinator \
  --cpus=2 --memory=4g \
  -v "$WORK_DIR/output:/output:rw" \
  -v "$WORK_DIR/prompt-coordinator.md:/agent-data/prompt.md:ro" \
  -e "ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY" \
  ateam-test \
  bash -c '
    claude -p "$(cat /agent-data/prompt.md)" \
      --dangerously-skip-permissions \
      --output-format stream-json \
      --max-budget-usd 0.50 \
      | tee /output/coordinator-stream.jsonl
  '
```

Note: the coordinator doesn't need `/workspace` mounted — it only reads reports and writes decisions. In the real system it would run on the host, not in Docker, but for this test Docker is fine.

**What to check:**

```bash
cat "$WORK_DIR/output/decisions.md"
```

**Questions to answer:**
- Are the decisions reasonable? Does it correctly identify what was already implemented?
- Does it follow the output format?
- Is the "what to focus on next time" useful? (This feeds into knowledge.md)
- Cost: how much did the coordinator run cost? It should be cheap since it's just reading and deciding.

---

## Summary of Outputs

After running all steps, you should have:

```
$WORK_DIR/
  bare.git/              # bare clone
  code/                  # worktree with agent's test changes
    node_modules/        # persisted across runs
  data/                  # persistent data dir (empty for this test)
  artifacts/             # build artifacts (empty for this test)
  output/
    report.md            # Step 3: audit findings
    actions.md           # Step 4: what was implemented
    decisions.md         # Step 7: coordinator triage
    stream.jsonl         # last agent's execution trace
    coordinator-stream.jsonl
    exit_code
    stderr.log
  Dockerfile
  prompt-audit.md
  prompt-implement.md
  prompt-coordinator.md
```

## Decision Points

Based on the results, decide:

| Question | If Yes | If No |
|---|---|---|
| Does `claude -p` with stream-json work reliably in Docker? | Proceed with spec as-is | Investigate alternative invocation modes |
| Are audit reports useful and well-structured? | Prompt templates are solid | Iterate on role.md prompts before building framework |
| Does the implement agent follow audit recommendations? | Report → action cycle works | May need stronger output contracts or structured data |
| Can we parse cost from stream-json? | Budget tracking works as designed | Need fallback (time-based estimation or external billing API) |
| Does persistent workspace speed up subsequent runs? | Worth the complexity of workspace/ management | Ephemeral worktrees may be simpler |
| Does the coordinator make reasonable decisions? | Coordinator pattern validated | May need more structured decision format or cheaper model |

## Estimated Cost

Rough estimates for the full validation against a medium-sized project:

| Step | Budget Cap | Expected |
|---|---|---|
| Step 3: Audit | $2.00 | ~$0.50–1.50 |
| Step 4: Implement | $3.00 | ~$1.00–2.50 |
| Step 5: Second audit | $2.00 | ~$0.50–1.50 |
| Step 7: Coordinator | $0.50 | ~$0.05–0.20 |
| **Total** | **$7.50** | **~$2–6** |
