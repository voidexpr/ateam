package sandboxprobe

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/ateam/internal/sandboxpolicy"
)

// Merge joins check lists into one. A file target that two lists name
// (through a link or not) is checked once, under the first list's id; a
// later expectation is kept when the first has none.
func Merge(lists ...[]Check) []Check {
	var merged []Check
	index := map[string]int{}
	for _, c := range slices.Concat(lists...) {
		key := c.ID
		if c.Method == MethodFile {
			key = "file:" + sandboxpolicy.RealPath(c.Target)
		}
		if at, dup := index[key]; dup {
			if len(merged[at].Expect) == 0 {
				merged[at].Expect, merged[at].ExpectWhy = c.Expect, c.ExpectWhy
			}
			continue
		}
		index[key] = len(merged)
		merged = append(merged, c)
	}
	return merged
}

// Selection is what --only, --skip and --with ask for: group names and
// check ids.
type Selection struct {
	Only, Skip, With []string
}

// Select returns the checks to run and how many were left out by Skip. An
// active check runs only when With names it. A name that is
// neither a group nor an id is an error.
func Select(checks []Check, sel Selection) (selected []Check, skipped int, err error) {
	for _, name := range slices.Concat(sel.Only, sel.Skip, sel.With) {
		known := slices.Contains(Groups, name) || slices.ContainsFunc(checks, func(c Check) bool { return c.ID == name })
		if !known {
			return nil, 0, fmt.Errorf("%q is neither a group nor a check: groups are %s; `ateam probe --list` shows the checks", name, strings.Join(Groups, ", "))
		}
	}
	names := func(list []string, c Check) bool {
		return slices.Contains(list, c.ID) || slices.Contains(list, c.Group)
	}
	for _, c := range checks {
		switch {
		case c.Active && !slices.Contains(sel.With, c.ID):
		case len(sel.Only) > 0 && !names(sel.Only, c):
		case names(sel.Skip, c):
			skipped++
		default:
			selected = append(selected, c)
		}
	}
	return selected, skipped, nil
}

// workers bounds the checks running at once: the network ones wait for a
// timeout.
const workers = 16

// Run carries out the checks and returns their results in the same order.
func Run(env Env, checks []Check) []Result {
	scripts, _ := Scripts()
	results := make([]Result, len(checks))
	var wg sync.WaitGroup
	slots := make(chan struct{}, workers)
	for i, c := range checks {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			results[i] = runOne(env, c, scripts)
		}()
	}
	wg.Wait()
	return results
}

func runOne(env Env, c Check, scripts []Script) Result {
	var perm Permission
	var detail string
	var forced Verdict
	switch c.Method {
	case MethodFile:
		perm, detail = CheckFile(c.Target, c.Before)
	case MethodProgramFile:
		perm, detail = checkProgramFile(c.Target)
	case MethodProgram:
		perm = Absent
		if path := env.lookPath(c.Target); path != "" {
			perm, detail = Present, env.name(path)
		}
	case MethodEnv:
		perm = Absent
		if env.Getenv(c.Target) != "" {
			perm = Set
		}
	case MethodDocker:
		perm, detail, forced = checkDocker(strings.Split(c.Target, "\n"))
	case MethodRoot:
		perm, detail = checkRoot()
	case MethodGroup:
		perm, detail = checkGroup(c.Target)
	case MethodCaps:
		perm, detail = checkCaps(c.Target)
	case MethodKnown:
		perm, detail = c.Known, c.KnownDetail
	case MethodScript:
		at := slices.IndexFunc(scripts, func(s Script) bool { return s.ID == c.Target })
		if at < 0 {
			perm, detail, forced = Fails, "unknown: no such probe script", OK
			break
		}
		perm, detail, forced = scripts[at].Run(env)
	default:
		perm, detail = CheckNet(c.Method, c.Target)
	}
	verdict := forced
	if verdict == "" {
		verdict = judge(c, perm)
	}
	switch verdict {
	case Risk:
		why := c.Why
		if len(c.Expect) > 0 {
			why = joinDetail(c.ExpectWhy, c.Why)
		}
		detail = joinDetail(detail, why)
	case Unmet:
		detail = joinDetail(detail, c.ExpectWhy)
	}
	detail = joinDetail(detail, c.Note)
	return Result{Check: c, Permission: perm, Verdict: verdict, Detail: detail}
}

// Counts are the totals of a run.
type Counts struct {
	Risk, Unmet, OK int
}

func Count(results []Result) Counts {
	var n Counts
	for _, r := range results {
		switch r.Verdict {
		case Risk:
			n.Risk++
		case Unmet:
			n.Unmet++
		default:
			n.OK++
		}
	}
	return n
}

// byGroup returns the results of each group, in report order: risks, then
// unmet, then ok, each in the order the checks were listed.
func byGroup(results []Result) map[string][]Result {
	rank := map[Verdict]int{Risk: 0, Unmet: 1, OK: 2}
	groups := map[string][]Result{}
	for _, r := range results {
		groups[r.Group] = append(groups[r.Group], r)
	}
	for _, list := range groups {
		sort.SliceStable(list, func(i, j int) bool { return rank[list[i].Verdict] < rank[list[j].Verdict] })
	}
	return groups
}
