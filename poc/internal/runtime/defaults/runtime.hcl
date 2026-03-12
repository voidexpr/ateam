agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  sandbox = "ateam_claude_sandbox_extra_settings.json"
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
