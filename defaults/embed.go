package defaults

import "embed"

// v1 prompt tree is the single source of truth. The legacy
// {roles,supervisor,report_base_prompt.md,code_base_prompt.md} entries
// that used to live here were dropped once all callers (cmd/*, runner,
// web, internal/prompts/embed.go) switched to read from prompts/.
//
//go:embed prompts/*.md prompts/report/*.md prompts/code/*.md
//go:embed ateam_claude_sandbox_extra_settings.json
//go:embed runtime.hcl Dockerfile config.toml
var FS embed.FS
