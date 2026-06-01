package flow

import "github.com/ateam/internal/runner"

// MultiReporter fans every Reporter callback to its children in declaration
// order. Nil children are silently skipped so callers can build a chain
// containing optional reporters without conditionals
// (e.g. JSONReporter only when --format jsonl).
//
// Each child owns its own thread-safety — same contract as a standalone
// Reporter. Children are called sequentially within one callback; a slow
// child delays its siblings on that callback. Fine at our scale.
type MultiReporter []Reporter

func (m MultiReporter) StageStart(s StageInfo) {
	for _, r := range m {
		if r != nil {
			r.StageStart(s)
		}
	}
}

func (m MultiReporter) StageEnd(s StageInfo, o StageOutcome) {
	for _, r := range m {
		if r != nil {
			r.StageEnd(s, o)
		}
	}
}

func (m MultiReporter) StepSkipped(parent StageInfo, stepName, reason string) {
	for _, r := range m {
		if r != nil {
			r.StepSkipped(parent, stepName, reason)
		}
	}
}

func (m MultiReporter) BundleStart(b BundleInfo) {
	for _, r := range m {
		if r != nil {
			r.BundleStart(b)
		}
	}
}

func (m MultiReporter) BundleEnd(b BundleInfo, res Result) {
	for _, r := range m {
		if r != nil {
			r.BundleEnd(b, res)
		}
	}
}

func (m MultiReporter) AgentEvent(b BundleInfo, p runner.RunProgress) {
	for _, r := range m {
		if r != nil {
			r.AgentEvent(b, p)
		}
	}
}
