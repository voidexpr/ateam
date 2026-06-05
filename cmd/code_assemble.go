package cmd

// SPEC INVARIANT (Next-round step 6): assembleAction is gone.
// `ateam prompt --action <action>` for unknown actions routes through
// NewSingleSupervisorBundle.

// SPEC INVARIANT (Next-round step 6): assembleCodeManagementV1 is
// gone. The code-management body composes via NewCodeBundle's
// PromptFile with {{dynamic.code_mgmt_review}} weaving the review
// content in.

// SPEC INVARIANT (post-step-10 cleanup): renderCLIWrapper is gone.
// CLI --post-prompt content renders through prompts.PromptText
// against a flow.Runtime — same single resolver path as the bundle
// body, no second BuildEngine-style engine constructed.
