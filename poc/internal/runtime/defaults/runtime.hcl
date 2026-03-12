agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
  env = {
    CLAUDECODE = ""
  }
}

agent "claude-sonnet" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--model", "sonnet", "--max-budget-usd", "0.50"]
  env = {
    CLAUDECODE = ""
  }
}

agent "claude-haiku" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose", "--model", "haiku", "--max-budget-usd", "0.10"]
  env = {
    CLAUDECODE = ""
  }
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

profile "test" {
  agent     = "mock"
  container = "none"
}
