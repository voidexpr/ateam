package assembler

import (
	"fmt"
	"strings"
)

// ValidateRoleName enforces the two restrictions from the spec:
//
//  1. Cannot start with `_` — that prefix is reserved for dir-level structural files.
//  2. Cannot end with `.pre` or `.post` — would create greedy-parse ambiguity
//     for fragment filenames.
//
// Otherwise role names are arbitrary dot-separated identifiers.
func ValidateRoleName(name string) error {
	if name == "" {
		return fmt.Errorf("role name is empty")
	}
	if strings.HasPrefix(name, "_") {
		return fmt.Errorf("role name %q cannot start with `_` (reserved for dir-level structural files)", name)
	}
	if strings.HasSuffix(name, ".pre") || strings.HasSuffix(name, ".post") {
		return fmt.Errorf("role name %q cannot end with `.pre` or `.post` (would make fragment parsing ambiguous)", name)
	}
	return nil
}
