locals {
  claude_sandbox = <<-EOF
  {
    "permissions": {
      "defaultMode": "acceptEdits",
      "additionalDirectories": [
        "~/Library/Caches"
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
        "WebSearch",
        "WebFetch"
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
        "ateam:*"
      ],
      "filesystem": {
        "allowWrite": [
          "/tmp",
          "~/.bun",
          "~/.cache",
          "~/.cache/bun",
          "~/.cache/cargo",
          "~/.cache/gradle",
          "~/.cache/git",
          "~/.cache/npm",
          "~/.cache/pip",
          "~/.cache/pnpm",
          "~/.cache/pypoetry",
          "~/.cache/uv",
          "~/.cache/yarn",
          "~/.cargo",
          "~/.cargo/git",
          "~/.cargo/registry",
          "~/.config/git",
          "~/.docker/cli-plugins",
          "~/.gradle",
          "~/.local/bin",
          "~/.local/share/pnpm",
          "~/.local/share/pypoetry",
          "~/.local/share/uv",
          "~/.m2",
          "~/.npm",
          "~/.npm/_npx",
          "~/.pnpm-store",
          "~/.yarn",
          "~/go",
          "~/go/pkg/mod",
          "~/Library/Caches",
          "~/Library/Caches/bun",
          "~/Library/Caches/cargo",
          "~/Library/Caches/go",
          "~/Library/Caches/go-build",
          "~/Library/Caches/gradle",
          "~/Library/Caches/npm",
          "~/Library/Caches/pip",
          "~/Library/Caches/pnpm",
          "~/Library/Caches/pypoetry",
          "~/Library/Caches/yarn",
          "~/Library/Containers/com.docker.docker",
          "~/Library/Group Containers/group.com.docker",
          "~/Library/pnpm"
        ],
        "allowRead": [
          "/Applications/Docker.app",
          "/var/lib/docker",
          "/var/run/docker.sock",
          "/bin",
          "/lib",
          "/opt",
          "/opt/homebrew/bin/bun",
          "/opt/homebrew/bin/docker",
          "/opt/homebrew/bin/git",
          "/opt/homebrew/bin/uv",
          "/opt/homebrew/Cellar/gradle",
          "/opt/homebrew/Cellar/go",
          "/opt/homebrew/Cellar/maven",
          "/opt/homebrew/bin/python3",
          "/opt/homebrew/lib/node_modules",
          "/usr",
          "/usr/bin/bun",
          "/usr/bin/docker",
          "/usr/bin/git",
          "/usr/bin/python3",
          "/usr/bin/uv",
          "/usr/lib/docker",
          "/usr/lib/git-core",
          "/usr/lib/go",
          "/usr/lib/node_modules",
          "/usr/lib/python3",
          "/usr/local/bin/bun",
          "/usr/local/bin/docker",
          "/usr/local/bin/git",
          "/usr/local/bin/uv",
          "/usr/local/Cellar/maven",
          "/usr/local/go",
          "/usr/local/lib/node_modules",
          "/usr/share/gradle",
          "/usr/share/maven",
          "/var/run/docker.sock",
          "~/.cargo/bin",
          "~/.cargo/config",
          "~/.cargo/config.toml",
          "~/.config/git",
          "~/.config/pip",
          "~/.gitconfig",
          "~/.git-credentials",
          "~/.local/bin",
          "~/.local/share/pnpm",
          "~/.local/share/pypoetry",
          "~/.local/share/uv",
          "~/.m2/settings.xml",
          "~/.npmrc",
          "~/.pip",
          "~/.poetry",
          "~/.config/pypoetry",
          "~/.yarnrc",
          "~/.yarnrc.yml"
        ]
      },
      "network": {
        "allowedDomains": [
          "*.github.com",
          "*.githubusercontent.com",
          "registry.npmjs.org",
          "api.anthropic.com",
          "pypi.org",
          "crates.io",
          "proxy.golang.org"
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
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
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

container "docker-persistent" {
  type        = "docker"
  mode        = "persistent"
  dockerfile  = "Dockerfile"
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

profile "docker-persistent" {
  agent     = "claude-docker"
  container = "docker-persistent"
}
