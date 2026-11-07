package runtime

import (
	"sort"
	"strconv"
	"strings"
)

// TraceEntry is one merged attribute with every layer that set it, in load
// order; the last contribution is the effective value.
type TraceEntry struct {
	Key   string
	Chain []Contribution
}

// Trace returns the recorded attributes whose key is prefix or starts with
// prefix followed by a dot, sorted by key. "" returns every attribute; a
// trailing ".*" or "." on prefix is ignored.
func (c *Config) Trace(prefix string) []TraceEntry {
	prefix = strings.TrimSuffix(strings.TrimSuffix(prefix, "*"), ".")
	var out []TraceEntry
	for key, chain := range c.Sources {
		if prefix != "" && key != prefix && !strings.HasPrefix(key, prefix+".") {
			continue
		}
		out = append(out, TraceEntry{Key: key, Chain: chain})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Where renders where a contribution was written: "<file>:<line>" with the
// home directory shortened to "~", "built-in:<line>" for the embedded
// defaults, and for an inherited value the base agent followed by the
// location that set it on the base.
func (c *Config) Where(con Contribution) string {
	loc := c.displayLayer(con.File)
	if con.Line > 0 {
		loc += ":" + strconv.Itoa(con.Line)
	}
	if con.Layer != con.File {
		return con.Layer + " at " + loc
	}
	return loc
}
