package sandboxprobe

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ateam/internal/sandboxpolicy"
)

// unlistedHost is a host no profile lists: reaching it shows the network
// rules do not hold.
const unlistedHost = "https://example.org"

// Permissions a policy expects, by what the rule opens.
var (
	expectExclude = closed
	expectRead    = safe(Read, Absent, Blocked)
	expectWrite   = safe(Write, Create)
)

// FromPolicy turns every rule of a profile's policy into a check with
// what the rule says the result should be.
func FromPolicy(env Env, p *sandboxpolicy.Policy) []Check {
	var checks []Check
	file := func(group, kind string, r sandboxpolicy.Rule, expect []Permission) {
		check := env.file(group, r.Path, "", nil)
		check.Expect, check.ExpectWhy = expect, ruleText(kind, r)
		checks = append(checks, check)
	}
	for _, r := range p.Writable() {
		file("profile", "writable", r, expectWrite)
	}
	for _, r := range p.Read {
		// A read rule asks for a readable path; it is also writable when a
		// writable folder holds it and no rule keeps it read-only.
		expect := safe(Read)
		if p.WritableRoot(r.Path) != "" && !slices.ContainsFunc(p.ReadOnly, func(ro sandboxpolicy.Rule) bool { return ro.Path == r.Path }) {
			expect = safe(Read, Write)
		}
		file("profile", "readable", r, append(expect, Absent))
	}
	for _, r := range p.ReadOnly {
		file("profile", "read-only", r, safe(Read, Absent))
	}
	for _, r := range p.Exclude {
		// ateam closes its own files in every profile; the others are what
		// the profile's author wants unread.
		group := "secrets"
		if r.Source == "ateam" {
			group = "ateam"
		}
		file(group, "excluded", r, expectExclude)
	}
	network := func(check Check, expect Permission, why string) {
		check.Group, check.ID = "profile", "profile:"+check.Name
		check.Expect, check.ExpectWhy = safe(expect), why
		checks = append(checks, check)
	}
	for _, host := range p.Domains {
		if strings.Contains(host, "*") {
			continue
		}
		network(URLCheck("profile", "https://"+host, MethodURLProxy), Connect, "the profile allows "+host)
	}
	for _, host := range p.DeniedDomains {
		if strings.Contains(host, "*") {
			continue
		}
		network(URLCheck("profile", "https://"+host, MethodURLProxy), Blocked, "the profile refuses "+host)
	}
	for _, method := range []Method{MethodURLProxy, MethodURLDirect} {
		network(URLCheck("profile", unlistedHost, method), Blocked, "no rule lists this host")
	}
	for _, port := range p.HostPorts {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		checks = append(checks, Check{ID: "profile:" + addr, Group: "profile", Type: TypeNetwork, Method: MethodDial, Target: addr, Name: addr + " (host port)"})
	}
	for _, l := range p.Listen {
		addr := l.Addr()
		network(Check{Type: TypeNetwork, Method: MethodListen, Target: addr, Name: addr + " (listen)"}, Listen, "the profile serves on "+addr)
	}
	return checks
}

func ruleText(kind string, r sandboxpolicy.Rule) string {
	text := "the profile makes it " + kind
	if kind == "excluded" {
		text = "the profile excludes it"
	}
	if r.Source != "" {
		text += " (" + r.Source + ")"
	}
	return text
}

// Cover gives each check that has no expectation of its own the one of
// the policy rule that covers its target: the nearest rule above a path,
// and for the network what the policy leaves open. A check the policy
// says nothing about keeps its built-in safe set.
func Cover(checks []Check, p *sandboxpolicy.Policy) []Check {
	for i, c := range checks {
		if len(c.Expect) > 0 {
			continue
		}
		switch {
		case c.Type == TypeFile && c.Method == MethodFile && c.Group != "sockets":
			checks[i].Expect, checks[i].ExpectWhy = coverPath(p, c.Target)
		case c.Type == TypeNetwork:
			checks[i].Expect, checks[i].ExpectWhy = coverNet(p, c)
		}
	}
	return checks
}

// coverPath returns every permission up to what the nearest rule above
// path opens: less than the rule allows is never unmet for a path the
// profile does not name. Only rules the profile states count: that a
// sandbox leaves the home readable does not make a readable ~/.ssh
// expected.
func coverPath(p *sandboxpolicy.Policy, path string) ([]Permission, string) {
	real := sandboxpolicy.RealPath(path)
	best, bestLen, why := -1, -1, ""
	consider := func(level int, kind string, rules []sandboxpolicy.Rule) {
		for _, r := range rules {
			covers := sandboxpolicy.Within(path, r.Path) || sandboxpolicy.Within(real, r.Path)
			// At the same depth the closing rule wins: an exclude over a grant.
			if covers && (len(r.Path) > bestLen || len(r.Path) == bestLen && level < best) {
				best, bestLen, why = level, len(r.Path), ruleText(kind, r)
			}
		}
	}
	consider(1, "readable", p.Read)
	consider(2, "writable", p.Writable())
	consider(1, "read-only", p.ReadOnly)
	consider(0, "excluded", p.Exclude)
	switch best {
	case 0:
		return expectExclude, why
	case 1:
		return expectRead, why
	case 2:
		return slices.Concat(expectRead, expectWrite), why
	}
	return nil, ""
}

// coverNet expects a built-in connection to fail unless the policy lists
// its host: a sandbox refuses every direct connection and every host that
// is not listed. Listening, the loopback and name resolution are not
// judged: backends differ on them without a rule saying so.
func coverNet(p *sandboxpolicy.Policy, c Check) ([]Permission, string) {
	switch c.Method {
	case MethodListen, MethodLoopback, MethodDNS:
		return nil, ""
	case MethodDial:
		if strings.HasPrefix(c.Target, "127.") || strings.HasPrefix(c.Target, "localhost:") {
			return nil, ""
		}
	case MethodURLProxy:
		host := strings.TrimPrefix(c.Target, "https://")
		if slices.Contains(p.Domains, host) && !slices.Contains(p.DeniedDomains, host) {
			return safe(Connect), "the profile allows " + host
		}
	}
	if p.NetworkClosed() {
		return safe(Blocked), "the profile has no network rule"
	}
	return safe(Blocked), "the profile does not list it"
}
