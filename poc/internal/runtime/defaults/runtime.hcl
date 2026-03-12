locals {
  claude_sandbox = <<-EOF
  {
    "permissions": {
      "defaultMode": "acceptEdits",
      "allow": [
        "Read", "Edit", "Write", "Glob", "Grep", "Bash(*)",
        "Agent", "NotebookEdit", "AskUserQuestion", "Skill",
        "EnterPlanMode", "ExitPlanMode", "EnterWorktree", "LSP",
        "SendMessage", "TaskCreate", "TaskGet", "TaskList",
        "TaskOutput", "TaskStop", "TaskUpdate", "TeamCreate",
        "TeamDelete", "WebSearch", "WebFetch"
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
        "git:*", "go:*", "cargo:*",
        "npm:*", "npx:*", "bun:*", "bunx:*", "pnpm:*", "yarn:*",
        "pip:*", "pip3:*", "uv:*", "poetry:*",
        "gradle:*", "mvn:*",
        "docker:*", "ateam:*"
      ],
      "filesystem": {
        "allowWrite": ["/tmp", "~/Library/Caches"]
      },
      "network": {
        "allowedDomains": [
          "*.github.com", "*.githubusercontent.com",
          "registry.npmjs.org", "api.anthropic.com"
        ],
        "allowLocalBinding": true
      }
    }
  }
  EOF
}

agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
  env = {
    CLAUDECODE = ""
  }
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet", "--max-budget-usd", "0.50"]
}

agent "claude-haiku" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "haiku", "--max-budget-usd", "0.10"]
}

// claude-docker has no sandbox settings — the container itself provides isolation.
// Uses --dangerously-skip-permissions for unattended tool use inside Docker.
agent "claude-docker" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"]
  env = {
    CLAUDECODE = ""
  }
}

// claude-isolated uses a project-local config dir (.ateam/.claude) instead of ~/.claude,
// providing full isolation for ateam-specific agent settings and auth tokens.
// config_dir: relative paths resolve from .ateam/, absolute paths are used as-is.
agent "claude-isolated" {
  base       = "claude"
  config_dir = ".claude"
}

agent "codex" {
  type    = "codex"
  command = "codex"
  args    = ["--sandbox", "workspace-write", "--ask-for-approval", "never"]
}

agent "mock" {
  type = "builtin"
}

container "none" {
  type = "none"
}

container "docker" {
  type        = "docker"
  dockerfile  = "Dockerfile"
  forward_env = [
    "ANTHROPIC_API_KEY",
    "CLAUDE_CODE_OAUTH_TOKEN",
  ]
}

profile "default" {
  agent     = "claude"
  container = "none"
}

profile "cheap" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "sonnet", "--max-budget-usd", "0.50"]
}

profile "cheapest" {
  agent            = "claude"
  container        = "none"
  agent_extra_args = ["--model", "haiku", "--max-budget-usd", "0.10"]
}

profile "isolated" {
  agent     = "claude-isolated"
  container = "none"
}

profile "codex" {
  agent     = "codex"
  container = "none"
}

profile "docker" {
  agent     = "claude-docker"
  container = "docker"
}

profile "test" {
  agent     = "mock"
  container = "none"
}
