agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = <<-EOF
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
  env = {
    CLAUDECODE = ""
  }
}

agent "claude-sonnet" {
  base    = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet", "--max-budget-usd", "0.50"]
}

agent "claude-haiku" {
  base    = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--model", "haiku", "--max-budget-usd", "0.10"]
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

profile "default" {
  agent     = "claude"
  container = "none"
}

profile "cheap" {
  agent     = "claude-sonnet"
  container = "none"
}

profile "cheapest" {
  agent     = "claude-haiku"
  container = "none"
}

profile "codex" {
  agent     = "codex"
  container = "none"
}

profile "test" {
  agent     = "mock"
  container = "none"
}
