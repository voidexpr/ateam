package defaults

import "embed"

// TODO: fix this before v1 — remove new_report_base_prompt.md once it merges into report_base_prompt.md.
//
//go:embed roles/*/report_prompt.md roles/*/code_prompt.md
//go:embed report_base_prompt.md code_base_prompt.md
//go:embed new_report_base_prompt.md
//go:embed supervisor/review_prompt.md supervisor/code_management_prompt.md supervisor/code_verify_prompt.md supervisor/report_commissioning_prompt.md supervisor/auto_setup_prompt.md supervisor/exec_debug_prompt.md
//go:embed ateam_claude_sandbox_extra_settings.json
//go:embed runtime.hcl Dockerfile config.toml
var FS embed.FS
