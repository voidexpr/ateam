package agents

// Agent represents a role-specific analysis agent.
type Agent struct {
	ID          string
	Description string
}

// AllAgentIDs is the ordered list of all known agent identifiers.
var AllAgentIDs = []string{
	"refactor_small",
	"refactor_architecture",
	"docs_internal",
	"docs_external",
	"basic_project_structure",
	"automation",
	"dependencies",
	"testing_basic",
	"testing_full",
	"security",
}

// Registry maps agent IDs to their metadata.
var Registry = map[string]Agent{
	"refactor_small": {
		ID:          "refactor_small",
		Description: "Small refactoring opportunities: naming, duplication, error handling",
	},
	"refactor_architecture": {
		ID:          "refactor_architecture",
		Description: "Architecture analysis: coupling, layering, abstractions, patterns",
	},
	"docs_internal": {
		ID:          "docs_internal",
		Description: "Internal documentation: architecture docs, code overview, dev guides",
	},
	"docs_external": {
		ID:          "docs_external",
		Description: "External documentation: README, installation, usage, API docs",
	},
	"basic_project_structure": {
		ID:          "basic_project_structure",
		Description: "Project structure: file organization, build system, conventions",
	},
	"automation": {
		ID:          "automation",
		Description: "CI/CD, linting, formatting, pre-commit hooks, build automation",
	},
	"dependencies": {
		ID:          "dependencies",
		Description: "Dependency health: outdated, unused, vulnerable, alternatives",
	},
	"testing_basic": {
		ID:          "testing_basic",
		Description: "Test coverage gaps, missing edge cases, basic test quality",
	},
	"testing_full": {
		ID:          "testing_full",
		Description: "Full test suite analysis: flaky tests, integration gaps, test architecture",
	},
	"security": {
		ID:          "security",
		Description: "Security: vulnerabilities, injection risks, secrets, auth patterns",
	},
}

// IsValid returns true if the agent ID is known.
func IsValid(id string) bool {
	_, ok := Registry[id]
	return ok
}

// ResolveAgentList expands "all" and validates agent IDs.
// Returns the resolved list or an error if any ID is invalid.
func ResolveAgentList(ids []string) ([]string, error) {
	var result []string
	for _, id := range ids {
		if id == "all" {
			return AllAgentIDs, nil
		}
		if !IsValid(id) {
			return nil, &UnknownAgentError{ID: id}
		}
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, &NoAgentsError{}
	}
	return result, nil
}

type UnknownAgentError struct {
	ID string
}

func (e *UnknownAgentError) Error() string {
	return "unknown agent: " + e.ID
}

type NoAgentsError struct{}

func (e *NoAgentsError) Error() string {
	return "no agents specified"
}
