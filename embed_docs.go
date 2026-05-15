package main

import (
	_ "embed"

	"github.com/ateam/defaults"
)

//go:embed README.md
var readmeDoc string

//go:embed COMMANDS.md
var commandsDoc string

//go:embed CONFIG.md
var configDoc string

//go:embed ISOLATION.md
var isolationDoc string

//go:embed ROLES.md
var rolesDoc string

func init() {
	defaults.SelfDocs["README"] = readmeDoc
	defaults.SelfDocs["COMMANDS"] = commandsDoc
	defaults.SelfDocs["CONFIG"] = configDoc
	defaults.SelfDocs["ISOLATION"] = isolationDoc
	defaults.SelfDocs["ROLES"] = rolesDoc
}
