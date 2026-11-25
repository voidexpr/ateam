// Package sandboxprobe checks what the environment a process runs in can
// read, write, replace, reach and do. It loads no configuration: the
// built-in checks come from the binary, the rest from its caller.
package sandboxprobe

import (
	"slices"
	"strings"
)

// Type is what a check looks at: the report's type column.
type Type string

const (
	TypeFile       Type = "file"
	TypeNetwork    Type = "network"
	TypeCapability Type = "capability"
	TypeEnv        Type = "env"
)

// Permission is what a check found: the report's permission column.
type Permission string

const (
	Absent  Permission = "absent"  // nothing there, and it cannot be created
	Create  Permission = "create"  // nothing there, the folder above accepts a new entry
	Blocked Permission = "blocked" // there (or hidden), not readable; a refused connection
	Read    Permission = "read"
	Write   Permission = "write"
	Connect Permission = "connect"
	Listen  Permission = "listen"
	Set     Permission = "set"
	Present Permission = "present" // a capability that is there, not exercised
	Works   Permission = "works"   // an active check exercised it
	Fails   Permission = "fails"
)

// level orders permissions by how much they open: a result above what a
// policy expects is a risk, one below is unmet.
func (p Permission) level() int {
	switch p {
	case Read:
		return 1
	case Write, Create, Connect, Listen, Set, Present, Works:
		return 2
	}
	return 0
}

type Verdict string

const (
	OK    Verdict = "ok"
	Risk  Verdict = "RISK"
	Unmet Verdict = "UNMET"
)

// Method is how a check is carried out.
type Method string

const (
	MethodFile        Method = "file"
	MethodProgramFile Method = "program-file" // the file of a program and its folder: replaceable?
	MethodProgram     Method = "program"      // found in PATH?
	MethodListen      Method = "listen"
	MethodLoopback    Method = "loopback"
	MethodDial        Method = "dial"
	MethodURLProxy    Method = "url-proxy"
	MethodURLDirect   Method = "url-direct"
	MethodDNS         Method = "dns"
	MethodEnv         Method = "env"
	MethodDocker      Method = "docker"
	MethodRoot        Method = "root"
	MethodGroup       Method = "group"
	MethodCaps        Method = "linux-capabilities"
	MethodKnown       Method = "known" // found while building the list: Known holds the result
	MethodScript      Method = "script"
)

// Facts is what could be seen of a file target from outside a sandbox,
// before the checks run inside it.
type Facts struct {
	Exists bool `json:"exists"`
	IsDir  bool `json:"is_dir,omitempty"`
	Empty  bool `json:"empty,omitempty"` // a folder without entry, a file without content
}

// Check is one thing to find out. Target is what the method works on (an
// absolute path, host:port, a URL, a name); Name is what the report shows.
type Check struct {
	ID     string `json:"id"`
	Group  string `json:"group"`
	Type   Type   `json:"type"`
	Method Method `json:"method"`
	Target string `json:"target"`
	Name   string `json:"name"`
	Why    string `json:"why,omitempty"`
	// Safe lists the permissions that are no risk; nil means the result is
	// reported, not judged.
	Safe []Permission `json:"safe,omitempty"`
	// Expect is what a profile's policy says the result should be, with
	// the rule that says so. It replaces Safe.
	Expect    []Permission `json:"expect,omitempty"`
	ExpectWhy string       `json:"expect_why,omitempty"`
	Before    *Facts       `json:"before,omitempty"`
	// Note is added to the detail whatever the verdict.
	Note string `json:"note,omitempty"`
	// Quiet hides the row while its verdict is ok.
	Quiet bool `json:"quiet,omitempty"`
	// Active marks a check that starts something: off unless asked for.
	Active bool   `json:"active,omitempty"`
	Starts string `json:"starts,omitempty"`
	// Known is the result of a MethodKnown check.
	Known       Permission `json:"known,omitempty"`
	KnownDetail string     `json:"known_detail,omitempty"`
}

// Result is a check with what it found.
type Result struct {
	Check
	Permission Permission
	Verdict    Verdict
	Detail     string
}

// Groups lists the groups in the order the report prints them.
var Groups = []string{
	"profile", "paths", "where", "secrets", "shell", "agent", "ateam", "git",
	"programs", "sockets", "docker", "playwright", "identity", "mounts", "network",
}

func safe(perms ...Permission) []Permission { return perms }

// closed is the safe set of a path nothing should read.
var closed = safe(Blocked, Absent)

// readOnly is the safe set of a path that may be read, not changed.
var readOnly = safe(Read, Blocked, Absent)

// judge compares a permission with what the check expects.
func judge(c Check, perm Permission) Verdict {
	if len(c.Expect) > 0 {
		if slices.Contains(c.Expect, perm) {
			return OK
		}
		top := 0
		for _, e := range c.Expect {
			top = max(top, e.level())
		}
		if perm.level() > top {
			return Risk
		}
		return Unmet
	}
	if c.Safe != nil && !slices.Contains(c.Safe, perm) {
		return Risk
	}
	return OK
}

// joinDetail joins the parts that are not empty.
func joinDetail(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), "; ")
}
