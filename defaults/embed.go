package defaults

import "embed"

// v1 prompt tree (dual-shipped during the refactor — same content as the
// legacy paths above, accessible at the v1 paths the new assembler reads).
// The legacy entries can be removed once all callers have switched.
//
//go:embed roles/*/report_prompt.md roles/*/code_prompt.md
//go:embed report_base_prompt.md code_base_prompt.md
//go:embed supervisor/review_prompt.md supervisor/code_management_prompt.md supervisor/code_verify_prompt.md supervisor/report_auto_roles_prompt.md supervisor/auto_setup_prompt.md supervisor/exec_debug_prompt.md
//go:embed prompts/*.md prompts/report/*.md prompts/code/*.md
//go:embed ateam_claude_sandbox_extra_settings.json
//go:embed runtime.hcl Dockerfile config.toml
var FS embed.FS
