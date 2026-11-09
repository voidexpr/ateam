// An agent says how to run a CLI (command, args, env, sandbox, config_dir,
// pricing, required_env, model_pattern, the inside_container /
// outside_container blocks). A profile says what to run it with: agent,
// container, model, effort, max_budget_usd. An agent may still pin model /
// effort / max_budget_usd; the profile's values win, the --model flag wins
// over both, and `ateam env` shows the chain.

// Sandbox template for Claude agents.
// At runtime, the runner also grants access to (see writeSettings in runner.go):
//   - working directory (project source dir)
//   - .ateam/ project directory
//   - .ateamorg/ organization directory (if present)
//   - agent rw_paths / ro_paths / denied_paths from agent config blocks
locals {
  // Process env shared by every claude agent (agents with `base = "claude"`
  // inherit it; the other claude blocks below reference it explicitly).
  //
  // CLAUDECODE is unset so claude does not think it is nested in itself.
  //
  // CLAUDE_CODE_DISABLE_BACKGROUND_TASKS removes the Bash tool's
  // run_in_background parameter: a headless (`claude -p`) session kills
  // every background task the moment the model ends its turn, and the way
  // to wait on one changes between releases, so long commands must block
  // in one foreground call instead. Bash timeouts stay at the harness
  // defaults here; `ateam code` raises BASH_DEFAULT_TIMEOUT_MS /
  // BASH_MAX_TIMEOUT_MS for the supervisor only, above the sub-run
  // timeout, so a whole sub-run fits in one call — see cmd/code.go.
  claude_env = {
    CLAUDECODE = ""
    # EXPERIMENTAL: experiencing fork bombs with claude code (inside or outside of ateam)
    #               so trying to force usage of zsh to confirm/rule out bash config
    CLAUDE_CODE_SHELL = "/bin/zsh"
    CLAUDE_CODE_DISABLE_BACKGROUND_TASKS = "1"
  }

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
          "proxy.golang.org",
          "vuln.go.dev"
        ],
        "allowLocalBinding": true
      }
    }
  }
  EOF
}

// Detection of an outer Docker/Podman container, via /.dockerenv and
// /run/.containerenv. When detected, agents take the in-container code
// path: their inside_container block applies and outside_container does
// not, so claude/codex skip their own inner sandbox (nested sandboxes
// generally fail).
//
// Default true — these markers are dead-reliable. The toggle exists
// for symmetry with sandbox_detection and for edge cases (e.g. testing
// the host-execution code path from inside a container). Override
// per-invocation with --docker-detection.
//
// Always-on (cannot be disabled here): ATEAM_IN_CONTAINER=1.
docker_detection = true

// Detection of an outer NON-container sandbox — any process-level
// isolation layer that wraps ateam itself. The implementation is
// signal-driven, not tool-name-driven, so it picks up anything that
//   - exports a known env var: FENCE_SANDBOX, FIREJAIL_NAME, container=…
//   - applies macOS Seatbelt (caught by an ~46ms sandbox-exec probe —
//     fires for fence, sandbox-exec wrappers, App Sandbox apps,
//     Claude Code's own session, etc.)
//   - puts the process in a divergent Linux user namespace, or sets a
//     Seccomp filter / NoNewPrivs flag (caught by /proc reads — fires
//     for bubblewrap, firejail, Docker, Podman, systemd hardening, …)
//
// When detected with this toggle on, ateam treats the outer sandbox
// as a container, so the agent skips its inner sandbox (same logic as
// docker_detection — nested sandboxes generally fail).
//
// Default false: the underlying signals can have false positives that
// silently drop defense in depth. For example, systemd-hardened user
// services trip the Linux NoNewPrivs / Seccomp probe without actually
// being inside an outer sandbox; a false positive there would tell
// the agent to run with --dangerously-skip-permissions on a bare host.
//
// Opt in (true) when you knowingly run ateam under an outer sandbox
// (e.g. fence on macOS or Linux, firejail, sandbox-exec wrapper,
// bubblewrap, …) — set it in .ateamorg/runtime.hcl so the choice is
// versioned. When this is false and ateam detects probable outer
// isolation anyway, it prints a one-line warning before each agent run
// pointing at this setting.
//
// Always-on (cannot be disabled here): ATEAM_IN_SANDBOX=1.
//
// Detection layers gated by this setting:
//   - Cooperative env vars: FENCE_SANDBOX, FIREJAIL_NAME, container=...
//   - macOS sandbox-exec probe (~46ms once per startup, cached).
//   - Linux /proc heuristics: user-namespace divergence, Seccomp
//     filter active, NoNewPrivs flag.
//
// Run `ateam env` to see the detected source and current toggle state.
sandbox_detection = false

// A pinned model whose output price (USD per million output tokens, from
// the agent's pricing block) is above this value triggers a one-line
// warning at launch and in `ateam env`. The run still happens. Set to 0
// to disable the warning. An unpinned model always warns, since the
// agent CLI then picks its own default.
expensive_output_per_mtok = 20

// When the pricing tables below were last checked against the providers.
// `ateam env` warns once this is older than 90 days; `ateam setup
// --refresh-models` fetches the current tables from the ateam repository.
pricing_as_of = "2026-11-09"

// share default local claude config and adds a sandbox
agent "claude" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = local.claude_sandbox
  env     = local.claude_env
  required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]

  // A model that is not in the pricing table below must still look like
  // a claude id (claude-<family>-<version>, any version), else the run is
  // refused with the list of known ids and this page.
  model_pattern = "^claude-(fable|opus|sonnet|haiku)-[0-9][0-9.-]*$"
  model_docs    = "https://platform.claude.com/docs/en/models/overview"

  // Inside a container the container is the isolation: skip the
  // permission prompts and drop the sandbox settings file. Outside, the
  // sandbox above applies. Derived agents inherit the block.
  inside_container {
    args_append = ["--dangerously-skip-permissions"]
    sandbox     = ""
  }

  // Claude reports its own cost in the stream; this table serves the
  // expensive-model warning and cost estimates for runs that crashed
  // before a result event. No default_model on purpose: with no model
  // pinned, ateam must not pretend to know what the CLI will pick.
  // The bare aliases (opus, sonnet, haiku) are what the claude CLI
  // accepts in --model and resolve to the current generation.
  pricing {
    // What `ateam setup --model` proposes: the tier below the top model, in
    // the current generation. Opus is the default of the CLI itself.
    suggested_model = "claude-sonnet-5-5"

    model "claude-fable-5-1" {
      input_per_mtok        = 10.00
      cached_input_per_mtok = 0.25
      output_per_mtok       = 50.00
    }
    model "claude-opus-5-5" {
      input_per_mtok        = 4.00
      cached_input_per_mtok = 0.20
      output_per_mtok       = 20.00
    }
    model "opus" {
      input_per_mtok        = 4.00
      cached_input_per_mtok = 0.20
      output_per_mtok       = 20.00
    }
    model "claude-opus-5" {
      input_per_mtok        = 5.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 25.00
    }
    model "claude-opus-4-8" {
      input_per_mtok        = 5.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 25.00
    }
    model "claude-opus-4-7" {
      input_per_mtok        = 5.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 25.00
    }
    model "claude-opus-4-6" {
      input_per_mtok        = 5.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 25.00
    }
    model "claude-sonnet-5-5" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.10
      output_per_mtok       = 10.00
    }
    model "sonnet" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.10
      output_per_mtok       = 10.00
    }
    model "claude-sonnet-5" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.20
      output_per_mtok       = 10.00
    }
    model "claude-sonnet-4-6" {
      input_per_mtok        = 3.00
      cached_input_per_mtok = 0.30
      output_per_mtok       = 15.00
    }
    model "claude-haiku-5-5" {
      input_per_mtok        = 0.10
      cached_input_per_mtok = 0.01
      output_per_mtok       = 0.50
    }
    model "haiku" {
      input_per_mtok        = 0.10
      cached_input_per_mtok = 0.01
      output_per_mtok       = 0.50
    }
    model "claude-haiku-4-5" {
      input_per_mtok        = 1.00
      cached_input_per_mtok = 0.10
      output_per_mtok       = 5.00
    }
  }
}

agent "claude-auto" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--permission-mode" , "auto"]
  sandbox = local.claude_sandbox
  env     = local.claude_env
  required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]
}

// danger: no sandbox, could be useful for debugging
agent "claude-no-sandbox" {
  type    = "claude"
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  env     = local.claude_env
  required_env = ["CLAUDE_CODE_OAUTH_TOKEN|ANTHROPIC_API_KEY"]
}

// Model, effort and budget variants are profiles (cheap, cheapest, high,
// xhigh below), not agents. An agent may still pin them, never with
// `--model` inside args: attributes can be overridden one at a time and
// `ateam env` can say which file set them.

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
  // All codex args are passed to `codex exec --json` as subcommand flags
  // (see internal/agent/codex.go). --ask-for-approval is a top-level-only
  // flag with no effect on non-interactive exec, so it's not configured.
  // The runner now substitutes {{exec.prompt_file}} to the archived
  // prompt path; finishing the migration to a fully declarative args
  // list requires updating internal/agent/codex.go to pass the file
  // path instead of the resolved prompt text. Until then this stays
  // empty and the agent appends `req.Prompt` itself.
  //   args = ["--json", "--ask-for-approval", "never", "exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "{{exec.prompt_file}}"]
  args    = []
  required_env = ["OPENAI_API_KEY"]

  // gpt-<version>[-variant] or o<n>[-variant]; see model_pattern on the
  // claude agent for what the check does.
  model_pattern = "^(gpt-[0-9][0-9a-z.-]*|o[0-9]+(-[a-z]+)*)$"
  model_docs    = "https://platform.openai.com/docs/models"

  // On the host codex sandboxes itself; inside a container it must not.
  outside_container {
    args_append = ["--sandbox", "workspace-write", "--skip-git-repo-check"]
  }

  pricing {
    // The codex CLI's own default (first entry of its model list).
    default_model = "gpt-6.1-sol"
    // The tier below gpt-6-astra in the current generation.
    suggested_model = "gpt-6.1-sol"

    // Prices from https://developers.openai.com/api/docs/pricing, standard
    // tier, prompts up to 272K tokens. cached_input_per_mtok is set on every
    // model so cost estimates aren't inflated when the cache is warm — codex
    // sessions hit cache aggressively and EstimateCost is the only cost
    // source for codex (no result-event cost).
    //
    // GPT-6 generation: astra (top), sol, terra, luna (cheapest). The 5.6
    // sol/terra/luna line stays selectable in the CLI during the rollout.
    model "gpt-6-astra" {
      input_per_mtok        = 10.00
      cached_input_per_mtok = 1.00
      output_per_mtok       = 50.00
    }

    model "gpt-6.1-sol" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.10
      output_per_mtok       = 10.00
    }

    model "gpt-6-sol" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.20
      output_per_mtok       = 10.00
    }

    model "gpt-6.1-terra" {
      input_per_mtok        = 1.00
      cached_input_per_mtok = 0.05
      output_per_mtok       = 5.00
    }

    model "gpt-6-luna" {
      input_per_mtok        = 0.10
      cached_input_per_mtok = 0.01
      output_per_mtok       = 0.50
    }

    model "gpt-5.6-sol" {
      input_per_mtok        = 4.00
      cached_input_per_mtok = 0.40
      output_per_mtok       = 20.00
    }

    model "gpt-5.6-terra" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.20
      output_per_mtok       = 12.00
    }

    model "gpt-5.6-luna" {
      input_per_mtok        = 0.20
      cached_input_per_mtok = 0.02
      output_per_mtok       = 1.20
    }

    // Earlier generations still served by the API.
    model "gpt-5.5" {
      input_per_mtok        = 5.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 30.00
    }

    model "gpt-5.4" {
      input_per_mtok        = 2.50
      cached_input_per_mtok = 0.25
      output_per_mtok       = 15.00
    }

    model "gpt-5.4-mini" {
      input_per_mtok        = 0.75
      cached_input_per_mtok = 0.075
      output_per_mtok       = 4.50
    }

    model "gpt-5.4-nano" {
      input_per_mtok        = 0.20
      cached_input_per_mtok = 0.02
      output_per_mtok       = 1.25
    }

    model "gpt-5.3-codex" {
      input_per_mtok        = 1.75
      cached_input_per_mtok = 0.175
      output_per_mtok       = 14.00
    }

    model "gpt-5.2" {
      input_per_mtok        = 1.75
      cached_input_per_mtok = 0.175
      output_per_mtok       = 14.00
    }

    model "gpt-5.1" {
      input_per_mtok        = 1.25
      cached_input_per_mtok = 0.125
      output_per_mtok       = 10.00
    }

    model "gpt-5" {
      input_per_mtok        = 1.25
      cached_input_per_mtok = 0.125
      output_per_mtok       = 10.00
    }

    model "gpt-5-mini" {
      input_per_mtok        = 0.25
      cached_input_per_mtok = 0.025
      output_per_mtok       = 2.00
    }

    model "gpt-5-nano" {
      input_per_mtok        = 0.05
      cached_input_per_mtok = 0.005
      output_per_mtok       = 0.40
    }

    model "gpt-4.1" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 8.00
    }

    model "gpt-4.1-mini" {
      input_per_mtok        = 0.40
      cached_input_per_mtok = 0.10
      output_per_mtok       = 1.60
    }

    model "gpt-4o" {
      input_per_mtok        = 2.50
      cached_input_per_mtok = 1.25
      output_per_mtok       = 10.00
    }

    model "gpt-4o-mini" {
      input_per_mtok        = 0.15
      cached_input_per_mtok = 0.075
      output_per_mtok       = 0.60
    }

    model "o3" {
      input_per_mtok        = 2.00
      cached_input_per_mtok = 0.50
      output_per_mtok       = 8.00
    }
  }
}

// Codex model variants: see the codex-high / codex-xhigh profiles.

// codex-tmux: drives the interactive Codex TUI through tmux so slash commands
// unavailable to `codex exec` can run unattended. Example:
//   ateam exec "/review" --agent codex-tmux
agent "codex-tmux" {
  base    = "codex"
  type    = "codex-tmux"
  command = "codex"
  args    = ["--no-alt-screen", "-s", "workspace-write", "-a", "never", "-c", "check_for_update_on_startup=false", "--disable", "apps", "--disable", "plugins"]
  // model and effort: see the codex-tmux profile.

  start_timeout     = "15s"
  busy_timeout      = "20m"
  quiescence_window = "2s"
  // 300x100 follows oauth-cli-coder's tested choice: long `/review` output
  // wraps at narrower widths and breaks the prompt regex.
  tmux_width        = 300
  tmux_height       = 100

  // Interactive Codex normally reuses ~/.codex auth; don't require
  // OPENAI_API_KEY inherited from the headless codex agent.
  required_env = []

  // The base's outside_container { args_append } adds to the base's args;
  // this agent has its own args, so it does not apply here (same rule as
  // a plain args_append on a base), and nothing needs resetting.
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

// docker-oauth: mounts host ~/.claude/ read-only so OAuth tokens work.
// OAuth tokens are session-scoped and need the credential store in ~/.claude/.
container "docker-oauth" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

// docker-api: uses ANTHROPIC_API_KEY only (stateless, no ~/.claude/ needed).
// Pay-per-use but simpler — no session context required.
container "docker-api" {
  type        = "docker"
  dockerfile  = "Dockerfile"
  forward_env = [
    "ANTHROPIC_API_KEY",
  ]
}

// docker: default Docker container, same as docker-oauth.
container "docker" {
  type                = "docker"
  dockerfile          = "Dockerfile"
  mount_claude_config = true
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

// Codex has no `latest`-style alias, so its profiles pin the version
// (gpt-6.1-sol is the CLI's own default; `codex` lists the models it
// knows in ~/.codex/models_cache.json). Claude profiles use the bare
// aliases the CLI resolves to the current generation.
profile "default" {
  agent     = "claude"
  container = "none"
}

profile "cheap" {
  agent          = "claude"
  container      = "none"
  model          = "sonnet"
  max_budget_usd = "0.50"
}

profile "cheapest" {
  agent          = "claude"
  container      = "none"
  model          = "haiku"
  max_budget_usd = "0.10"
}

// high: opus with high reasoning effort, for the hardest tasks.
profile "high" {
  agent     = "claude"
  container = "none"
  model     = "opus"
  effort    = "high"
}

// xhigh: opus with xhigh effort. Slow + expensive; reserve for the few
// prompts that need deeper reasoning than high.
profile "xhigh" {
  agent     = "claude"
  container = "none"
  model     = "opus"
  effort    = "xhigh"
}

profile "isolated" {
  agent     = "claude-isolated"
  container = "none"
}

profile "codex" {
  agent     = "codex"
  container = "none"
}

profile "codex-high" {
  agent     = "codex"
  container = "none"
  model     = "gpt-6.1-sol"
  effort    = "high"
}

profile "codex-xhigh" {
  agent     = "codex"
  container = "none"
  model     = "gpt-6.1-sol"
  effort    = "xhigh"
}

profile "codex-tmux" {
  agent     = "codex-tmux"
  container = "none"
  model     = "gpt-6.1-sol"
  effort    = "xhigh"
}

profile "docker" {
  agent     = "claude"
  container = "docker"
}

profile "docker-oauth" {
  agent     = "claude"
  container = "docker-oauth"
}

profile "docker-api" {
  agent     = "claude"
  container = "docker-api"
}

profile "test" {
  agent     = "mock"
  container = "none"
}

// docker-exec: exec into a user-managed container.
// Ateam does NOT build or start the container — use precheck for lifecycle.
// Container name is resolved from: --container-name flag > ateam secret > CONTAINER_NAME env.
// Set per-project: ateam secret CONTAINER_NAME=my-container --scope project
container "docker-exec" {
  type             = "docker-exec"
  docker_container = "{{container.name}}"
  forward_env = [
    "CLAUDE_CODE_OAUTH_TOKEN",
    "ANTHROPIC_API_KEY",
  ]
}

profile "docker-exec" {
  agent     = "claude"
  container = "docker-exec"
}

// Example custom docker-exec with explicit container name and precheck.
// docker_container also accepts `{{container.name}}` (or the legacy
// `{{CONTAINER_NAME}}` alias, deprecated) to defer the resolution to the
// runner-side substitution layer — handy when one container block is
// shared across profiles that bind different container names via
// `ateam secret CONTAINER_NAME=...`.
//   container "my-app" {
//     type             = "docker-exec"
//     docker_container = "my-app-dev"
//     precheck         = ["sh", "docker-precheck.sh"]
//     forward_env      = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"]
//     # Custom exec template (default: "docker exec {{container.name}} {{cmd}}").
//     # {{container.name}} expands to the resolved container; {{cmd}} expands
//     # to the agent command + args at exec time. Legacy ALL_CAPS spellings
//     # {{CONTAINER}} / {{CMD}} are still accepted but deprecated.
//     # exec = "podman exec {{container.name}} {{cmd}}"
//   }
//   profile "my-app" {
//     agent     = "claude"
//     container = "my-app"
//   }

// ---------------------------------------------------------------------
// Project settings. The same file holds them, so one layer can set a
// default (e.g. timeouts or a default_profile in ~/.ateam/ateam.hcl) and
// the project overrides single attributes. `ateam init` writes the
// project block with the name, which is what makes a .ateam/ directory a
// project (~/.ateam never is).
// ---------------------------------------------------------------------

project {
  // name = "myproject"       // set by ateam init; the project's identity
  // default_profile = ""     // profile for every run unless overridden below
  report_max_parallel    = 3
  report_timeout_minutes = 20
  review_timeout_minutes = 20
  code_timeout_minutes   = 120
  verify_timeout_minutes = 30
  exec_timeout_minutes   = 60
  // serve_port = 8080        // fixed port for `ateam serve` (default: random)
}

// Which roles `ateam report` runs by default. A role block can also pin
// a profile for that role: role "project.security" { profile = "docker" }.
role "code.bugs" { status = "on" }
role "code.recent" { status = "on" }
role "code.structure" { status = "on" }
role "critic.engineering" { status = "off" }
role "critic.features" { status = "off" }
role "critic.project" { status = "off" }
role "database.schema" { status = "off" }
role "design.architecture" { status = "on" }
role "docs.external" { status = "on" }
role "docs.followable" { status = "off" }
role "docs.internal" { status = "on" }
role "perf.benchmarks" { status = "off" }
role "perf.optimization" { status = "off" }
role "project.automation" { status = "on" }
role "project.dependencies" { status = "on" }
role "project.discover_cmd" { status = "off" }
role "project.maintenance" { status = "off" }
role "project.production_ready" { status = "off" }
role "project.security" { status = "on" }
role "test.blackbox" { status = "off" }
role "test.gaps" { status = "on" }
role "test.quality" { status = "off" }
role "test.recent" { status = "on" }

// Supervisor profiles (review, code, the code supervisor itself). Empty
// means the project default_profile, else the "default" profile.
//   supervisor {
//     review_profile          = "high"
//     code_profile            = "cheap"
//     code_supervisor_profile = "high"
//   }

// Extra sandbox grants and container settings for this project.
//   sandbox_extra {
//     allow_write          = ["/path/to/extra/writable/dir"]
//     allow_read           = ["/path/to/extra/readable/dir"]
//     allow_domains        = ["api.example.com"]
//     unsandboxed_commands = ["make:*"]
//   }
//   container_extra {
//     extra_args  = ["--network", "host"]
//     forward_env = ["MY_TOKEN"]
//     env         = { FOO = "bar" }
//   }
