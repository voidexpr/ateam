package runtime

// NativeConfig is what the agent CLI's own configuration would contribute
// to the model/effort decision if ateam passed no flags: claude's
// settings.json (and ANTHROPIC_MODEL), codex's config.toml. It is the
// lowest entry of a ModelReport chain. Read by internal/agent; kept here
// so the report can carry it without importing the agent package.
type NativeConfig struct {
	Model  string
	Effort string
	Source string // file or env var that set the values, or what was looked at
	Note   string // "not found", "not inspected (runs in a container)", parse error, …
}
