package sandboxprobe

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/ateam/internal/display"
)

// Environment is the report's header: where the checks ran.
type Environment struct {
	OS        string   `json:"os"`
	Isolation string   `json:"isolation"` // what shows a sandbox or a container; "" for none
	User      string   `json:"user"`
	Proxy     []string `json:"proxy"` // the proxy variables that are set
	TmpDir    string   `json:"tmpdir"`
}

func (e Environment) String() string {
	isolation, proxy := "none detected", "none"
	if e.Isolation != "" {
		isolation = e.Isolation
	}
	if len(e.Proxy) > 0 {
		proxy = strings.Join(e.Proxy, " ")
	}
	return fmt.Sprintf("%s, user: %s, proxy: %s, isolation: %s", e.OS, e.User, proxy, isolation)
}

// Report is a run, ready to print.
type Report struct {
	// Started lists what the run started: a sandbox, active checks. Empty
	// when it started nothing.
	Started     []string
	Environment Environment
	Results     []Result
	Skipped     int
}

// StartedLines returns the first lines of every output: what was started.
func StartedLines(started []string) []string {
	if len(started) == 0 {
		return []string{"nothing started: checking this environment"}
	}
	lines := make([]string, len(started))
	for i, s := range started {
		lines[i] = "started: " + s
	}
	return lines
}

// Header prints the first lines of a report: what was started, then where
// the checks run. It is printed before the checks: an active one may take
// a while, and what it starts is said first.
func Header(w io.Writer, started []string, env Environment) {
	for _, line := range StartedLines(started) {
		fmt.Fprintln(w, line)
	}
	fmt.Fprintln(w, env)
}

// TableOptions choose the rows of a table.
type TableOptions struct {
	All   bool // also the ok rows that are absent or quiet
	Quiet bool // only the rows that are not ok
	Color bool
}

// nameWidth caps the name column: a longer name pushes its own detail,
// not every row's.
const nameWidth = 48

// Table prints the results below a Header: grouped, risks first, with
// aligned columns. By
// default an ok row with nothing there is left out, and the ok rows of
// quiet checks are folded into one line per group.
func Table(w io.Writer, rep Report, opt TableOptions) {
	groups := byGroup(rep.Results)
	shown := func(r Result) bool {
		switch {
		case r.Verdict != OK:
			return true
		case opt.Quiet:
			return false
		}
		return opt.All || !nothingThere(r) && !r.Quiet
	}
	widths := [3]int{}
	// folded reports whether an ok row is printed joined with others.
	folded := func(r Result) bool { return !shown(r) && !opt.Quiet && !nothingThere(r) }
	for _, r := range rep.Results {
		if shown(r) || folded(r) {
			widths[0] = max(widths[0], len(r.Permission))
			widths[1] = max(widths[1], len(r.Type))
		}
		if shown(r) {
			widths[2] = max(widths[2], min(len(r.Name), nameWidth))
		}
	}
	paint := func(v Verdict, s string) string {
		switch v {
		case Risk:
			return display.Paint(opt.Color, s, display.StyleRed)
		case Unmet:
			return display.Paint(opt.Color, s, display.StyleYellow)
		}
		return display.Paint(opt.Color, s, display.StyleDim)
	}
	row := func(v Verdict, perm Permission, typ Type, name, detail string) {
		line := fmt.Sprintf("  %-*s  %-*s  %-*s", widths[0], perm, widths[1], typ, widths[2], name)
		if detail != "" {
			line += "  " + detail
		}
		line = strings.TrimRight(line, " ")
		// A row that needs a look keeps its text plain: only the verdict
		// is coloured. An ok row is dimmed whole.
		if v == OK {
			fmt.Fprintln(w, "  "+paint(v, fmt.Sprintf("%-5s", v)+line))
			return
		}
		fmt.Fprintln(w, "  "+paint(v, fmt.Sprintf("%-5s", v))+line)
	}
	hidden := 0
	for _, group := range Groups {
		var rows, joined []Result
		for _, r := range groups[group] {
			switch {
			case shown(r):
				rows = append(rows, r)
			case folded(r):
				joined = append(joined, r)
				hidden++
			default:
				hidden++
			}
		}
		if !opt.All {
			var creatable int
			rows, creatable = foldCreatable(rows)
			hidden += creatable
		}
		if len(rows)+len(joined) == 0 {
			continue
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w, display.Paint(opt.Color, group, display.StyleBold))
		for _, r := range rows {
			row(r.Verdict, r.Permission, r.Type, r.Name, r.Detail)
		}
		for _, f := range fold(joined) {
			row(OK, f.Permission, f.Type, f.Name, "")
		}
	}
	n := Count(rep.Results)
	fmt.Fprintln(w)
	total := fmt.Sprintf("%d RISK, %d UNMET, %d ok", n.Risk, n.Unmet, n.OK)
	if hidden > 0 && !opt.Quiet {
		total += fmt.Sprintf(" (%d not listed one by one: --all)", hidden)
	}
	fmt.Fprintf(w, "%s, %d skipped\n", total, rep.Skipped)
}

// nothingThere reports whether a result found no file: an ok row of that
// kind says little.
func nothingThere(r Result) bool {
	return r.Permission == Absent || r.Permission == Create
}

// creatableShown is how many risks of the kind "nothing there, it can be
// created" a group lists before joining them.
const creatableShown = 3

// foldCreatable joins the risks of a group that are paths with nothing
// there yet into one row, when there are many: they all follow from one
// writable folder above, which has a row of its own. It returns the rows
// and how many were joined.
func foldCreatable(rows []Result) ([]Result, int) {
	isCreatable := func(r Result) bool { return r.Verdict == Risk && r.Permission == Create && r.Type == TypeFile }
	var kept []Result
	n := 0
	for _, r := range rows {
		if isCreatable(r) {
			n++
		}
	}
	if n <= creatableShown {
		return rows, 0
	}
	joined := false
	for _, r := range rows {
		switch {
		case !isCreatable(r):
			kept = append(kept, r)
		case !joined:
			joined = true
			kept = append(kept, Result{Check: Check{Type: TypeFile, Name: fmt.Sprintf("%d paths", n)}, Permission: Create,
				Verdict: Risk, Detail: "nothing there yet, each can be created: --all"})
		}
	}
	return kept, n
}

// fold joins the ok rows of quiet checks: one row per permission and
// type, with the names of capabilities and the number of anything else.
func fold(results []Result) []Result {
	var folded []Result
	names := map[int][]string{}
	for _, r := range results {
		at := slices.IndexFunc(folded, func(f Result) bool { return f.Permission == r.Permission && f.Type == r.Type })
		if at < 0 {
			at = len(folded)
			folded = append(folded, Result{Check: Check{Type: r.Type}, Permission: r.Permission})
		}
		names[at] = append(names[at], r.Name)
	}
	for at := range folded {
		folded[at].Name = strings.Join(names[at], " ")
		if folded[at].Type != TypeCapability {
			folded[at].Name = fmt.Sprintf("%d more", len(names[at]))
		}
	}
	return folded
}

type jsonCheck struct {
	ID         string       `json:"id"`
	Group      string       `json:"group"`
	Type       Type         `json:"type"`
	Target     string       `json:"target"`
	Permission Permission   `json:"permission"`
	Expected   []Permission `json:"expected,omitempty"`
	Verdict    Verdict      `json:"verdict"`
	Detail     string       `json:"detail,omitempty"`
}

// JSON prints the whole report as one document. Ids of the fixed checks
// are the same on every machine, so two documents can be compared.
func JSON(w io.Writer, rep Report) error {
	doc := struct {
		Started     []string    `json:"started"`
		Environment Environment `json:"environment"`
		Skipped     int         `json:"skipped"`
		Checks      []jsonCheck `json:"checks"`
	}{Started: rep.Started, Environment: rep.Environment, Skipped: rep.Skipped, Checks: []jsonCheck{}}
	if doc.Started == nil {
		doc.Started = []string{}
	}
	for _, r := range rep.Results {
		expected := r.Expect
		if len(expected) == 0 {
			expected = r.Safe
		}
		doc.Checks = append(doc.Checks, jsonCheck{ID: r.ID, Group: r.Group, Type: r.Type, Target: r.Name,
			Permission: r.Permission, Expected: expected, Verdict: r.Verdict, Detail: r.Detail})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// List prints every check with whether it would run, and for an active
// one what it starts. With expected set (--dry-run) it also prints what
// each check that runs is compared with.
func List(w io.Writer, all, selected []Check, expected bool) {
	on := map[string]bool{}
	for _, c := range selected {
		on[c.ID] = true
	}
	for _, group := range Groups {
		printed := false
		for _, c := range all {
			if c.Group != group || expected && !on[c.ID] {
				continue
			}
			if !printed {
				fmt.Fprintf(w, "%s\n", group)
				printed = true
			}
			state := "off"
			if on[c.ID] {
				state = "on "
			}
			line := fmt.Sprintf("  %s  %-10s  %s", state, c.Type, c.ID)
			switch {
			case c.Active:
				line += "  (starts: " + c.Starts + ")"
			case !expected:
			case len(c.Expect) > 0:
				line += fmt.Sprintf("  expects %s: %s", permList(c.Expect), c.ExpectWhy)
			case c.Safe != nil:
				line += "  safe when " + permList(c.Safe)
			}
			fmt.Fprintln(w, line)
		}
	}
}

func permList(perms []Permission) string {
	names := make([]string, len(perms))
	for i, p := range perms {
		names[i] = string(p)
	}
	return strings.Join(names, "|")
}
