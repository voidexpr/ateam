agent "claude" {
  command = "claude"
  args    = ["-p", "--output-format", "stream-json", "--verbose"]
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

profile "test" {
  agent     = "mock"
  container = "none"
}
