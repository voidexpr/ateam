// Sandbox template for Claude agents.
// At runtime, the runner also grants access to (see writeSettings in runner.go):
//   - working directory (project source dir)
//   - .ateam/ project directory
//   - .ateamorg/ organization directory (if present)
//   - agent rw_paths / ro_paths / denied_paths from agent config blocks
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
      "allowAllUnixSockets": true,
      "excludedCommands": [
        "ateam:*"
      ],
      "filesystem": {
        "additionalDirectories": [
          "/tmp",
          "/var/folders",
          "/private/tmp",
          "~/.docker/run/"
        ],
        "allowWrite": [
          "~/.docker/run/",
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
          "~/.docker/run/",
          "/tmp",
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
          "/var/folders/",
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

// share default local claude config and adds a sandbox
agent "claude" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
  env = {
    CLAUDECODE = ""
  }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]

  // Inside containers: skip permissions (container provides isolation).
  // Outside containers: sandbox settings are applied automatically.
  args_inside_container    = ["--dangerously-skip-permissions"]
  sandbox_inside_container = false
}

agent "claude-auto" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--permission-mode" , "auto"]
  sandbox = local.claude_sandbox
  env = {
    CLAUDECODE = ""
  }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

// danger: no sandbox, could be useful for debugging
agent "claude-no-sandbox" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  env = {
    CLAUDECODE = ""
  }
  required_env = ["ANTHROPIC_API_KEY|CLAUDE_CODE_OAUTH_TOKEN"]
}

agent "claude-sonnet" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet", "--max-budget-usd", "0.50"]
}

agent "claude-haiku" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--model", "haiku", "--max-budget-usd", "0.10"]
}

// claude-docker: backward compatibility alias. The base claude agent now
// auto-detects containers and skips permissions/sandbox. Prefer using "claude" directly.
agent "claude-docker" {
  base = "claude"
  args = ["-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"]
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
  args    = ["--ask-for-approval", "never"]
  required_env = ["OPENAI_API_KEY"]

  args_outside_container = ["--sandbox", "workspace-write"]

  pricing {
    default_model = "gpt-5.3-codex"

    model "gpt-5.3-codex" {
      input_per_mtok  = 1.75
      output_per_mtok = 14.00
    }

    model "gpt-5.2-codex" {
      input_per_mtok  = 1.75
      output_per_mtok = 14.00
    }

    model "gpt-5.1-codex" {
      input_per_mtok  = 1.25
      output_per_mtok = 10.00
    }

    model "gpt-5-mini" {
      input_per_mtok  = 0.25
      output_per_mtok = 2.00
    }

    model "gpt-5-nano" {
      input_per_mtok  = 0.05
      output_per_mtok = 0.40
    }
  }
}

agent "mock" {
  type = "builtin"
  required_env = []

  pricing {
    default_model = "mock-default"

    model "mock-default" {
      input_per_mtok  = 1.00
      output_per_mtok = 2.00
    }
  }
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
  agent     = "claude"
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
  agent     = "claude"
  container = "docker-persistent"
}

// Docker Sandbox: runs agents inside a Docker AI sandbox (microVM).
// Provides hypervisor-level isolation with a private Docker daemon
// and bidirectional workspace sync (git root synced at same absolute path).
// Additional dirs (.ateamorg) are tar-copied as read-only snapshots.
// Requires: Docker Desktop 4.58+
//
// Directory strategy (vs regular Docker):
//   - Paths are 1:1 (same absolute path in sandbox as on host) — no remapping.
//   - Only ONE workspace dir is synced bidirectionally (git root).
//   - Extra dirs (.ateamorg, ~/.claude/) are tar-copied (one-way snapshots).
//   - No TranslatePath, SourceWritable, or HostCLIPath — not needed or supported.
//
// Networking:
//   - network_policy controls the sandbox VM's outbound access (default: "allow").
//   - The sandbox has its own Docker daemon. Containers inside that daemon have
//     restricted networking regardless of network_policy — they can pull images
//     but cannot make outbound HTTPS connections to arbitrary hosts.
//   - This means Docker-in-Docker builds (e.g. make test-docker) that need to
//     fetch packages during RUN steps will fail. Use the regular "docker" profile
//     for DinD workloads.
//
// Auto-recreation:
//   - A config hash is stored in .ateam/cache/. When the config or ateam binary
//     changes, the sandbox is automatically removed and recreated.
//
// copy_claude_config: when true, copies ~/.claude/ config (skills, plugins,
// settings.json, CLAUDE.md) into the sandbox so the inner agent has access
// to user-defined skills and MCP configuration. Default: false.
container "docker-sandbox" {
  type               = "docker-sandbox"
  copy_claude_config = false
  network_policy     = "allow"
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

profile "docker-sandbox" {
  agent     = "claude"
  container = "docker-sandbox"
}

// Devcontainer: runs agents inside the project's .devcontainer/ environment.
// The devcontainer must include the agent CLI (e.g. claude, codex).
// Requires: npm install -g @devcontainers/cli
container "devcontainer" {
  type        = "devcontainer"
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

profile "devcontainer" {
  agent     = "claude"
  container = "devcontainer"
}
