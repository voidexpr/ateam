package defaults

// SelfDocs maps a doc identifier (README, COMMANDS, CONFIG, ISOLATION, ROLES)
// to its embedded markdown content. Populated by package main at init() time
// because //go:embed cannot reach above the package directory and these docs
// live at the repo root.
var SelfDocs = map[string]string{}
