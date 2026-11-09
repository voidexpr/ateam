package runtime

// contextSides are the Sources key segments of the two context blocks an
// agent may carry.
var contextSides = []string{"inside_container.", "outside_container."}

// overlayAttrNames are the scalar and list attributes of a context block
// in a stable order; env and the args additions are handled apart.
var overlayAttrNames = []string{"args", "config_dir", "effort", "max_budget_usd", "model", "sandbox"}

func isContextSide(attr string) bool {
	return attr == "inside_container" || attr == "outside_container"
}

// overlayOf returns the block of ac for one side ("inside_container.").
func overlayOf(ac *AgentConfig, side string) *AgentOverlay {
	if side == contextSides[0] {
		return &ac.InsideContainer
	}
	return &ac.OutsideContainer
}

func overlayFromHCL(h hclOverlay) AgentOverlay {
	return AgentOverlay{
		Args:         h.Args,
		Env:          h.Env,
		Sandbox:      h.Sandbox,
		ConfigDir:    h.ConfigDir,
		Model:        h.Model,
		Effort:       h.Effort,
		MaxBudgetUSD: h.MaxBudgetUSD,
	}
}

// copyOverlayAttr copies one block attribute from src to dst and renders
// it for provenance.
func copyOverlayAttr(dst, src *AgentOverlay, attr string) string {
	switch attr {
	case "args":
		dst.Args = src.Args
		return renderList(src.Args)
	case "sandbox":
		dst.Sandbox = src.Sandbox
		return src.Sandbox
	case "config_dir":
		dst.ConfigDir = src.ConfigDir
		return src.ConfigDir
	case "model":
		dst.Model = src.Model
		return src.Model
	case "effort":
		dst.Effort = src.Effort
		return src.Effort
	case "max_budget_usd":
		dst.MaxBudgetUSD = src.MaxBudgetUSD
		return src.MaxBudgetUSD
	}
	return ""
}

// mergeOverlay merges one context block of a layer into o. key is
// "agent.<name>.<side>."; attributes are recorded under it.
func (c *Config) mergeOverlay(key, label string, o *AgentOverlay, h hclOverlay) {
	src := overlayFromHCL(h)
	present, attrs := presentAttrs(h.Body, overlaySchema)
	for attr, line := range present {
		switch attr {
		case "env":
			keyLines := mapKeyLines(attrs["env"], src.Env)
			for k, v := range src.Env {
				o.Env = setMapKey(o.Env, k, v)
				c.record(key+"env."+k, label, v, keyLines[k])
			}
		case "args_prepend":
			c.addList(key, attr, label, h.ArgsPrepend, line)
		case "args_append":
			c.addList(key, attr, label, h.ArgsAppend, line)
		default:
			c.record(key+attr, label, copyOverlayAttr(o, &src, attr), line)
		}
	}
}

// additions returns the *_prepend / *_append lists accumulated for key.
func (c *Config) additions(key string) (prepend, appended []string) {
	if a := c.adds[key]; a != nil {
		return a.prepend, a.append
	}
	return nil, nil
}

// inheritOverlay copies the base's block attributes the child does not
// set. The base's args additions are taken only when the child has no args
// of its own, the rule a plain args_append on a base follows; the child's
// own additions wrap them.
func (c *Config) inheritOverlay(childKey, baseKey, baseLabel string, child, base *AgentOverlay, childSetArgs bool) {
	if _, set := c.Sources[childKey+"args"]; set {
		childSetArgs = true // the block's own args replace the base's list as well
	}
	for _, attr := range overlayAttrNames {
		if _, set := c.Sources[childKey+attr]; set {
			continue
		}
		from, inBase := c.Source(baseKey + attr)
		if !inBase {
			continue
		}
		c.inherit(childKey+attr, baseLabel, copyOverlayAttr(child, base, attr), from)
	}
	for k, v := range base.Env {
		if _, set := c.Sources[childKey+"env."+k]; set {
			continue
		}
		from, _ := c.Source(baseKey + "env." + k)
		child.Env = setMapKey(child.Env, k, v)
		c.inherit(childKey+"env."+k, baseLabel, v, from)
	}
	if childSetArgs {
		return
	}
	if len(base.ArgsPrepend) > 0 {
		from, _ := c.Source(baseKey + "args_prepend")
		child.ArgsPrepend = append(append([]string(nil), child.ArgsPrepend...), base.ArgsPrepend...)
		c.inherit(childKey+"args_prepend", baseLabel, renderList(base.ArgsPrepend), from)
	}
	if len(base.ArgsAppend) > 0 {
		from, _ := c.Source(baseKey + "args_append")
		child.ArgsAppend = append(append([]string(nil), base.ArgsAppend...), child.ArgsAppend...)
		c.inherit(childKey+"args_append", baseLabel, renderList(base.ArgsAppend), from)
	}
}

// ForContext returns ac with its inside_container block applied when
// inside is true (ateam itself runs in a container, or the run launches
// one), else its outside_container block. Block attributes replace the
// agent's, the block's args additions wrap the result, and Context names
// the block applied.
func (c *Config) ForContext(ac AgentConfig, inside bool) AgentConfig {
	side := contextSides[1]
	if inside {
		side = contextSides[0]
	}
	o := *overlayOf(&ac, side)
	key := "agent." + ac.Name + "." + side
	for _, attr := range overlayAttrNames {
		if _, set := c.Sources[key+attr]; !set {
			continue
		}
		switch attr {
		case "args":
			ac.Args = o.Args
		case "sandbox":
			ac.Sandbox = o.Sandbox
		case "config_dir":
			ac.ConfigDir = o.ConfigDir
		case "model":
			ac.Model = o.Model
		case "effort":
			ac.Effort = o.Effort
		case "max_budget_usd":
			ac.MaxBudgetUSD = o.MaxBudgetUSD
		}
	}
	if len(o.ArgsPrepend)+len(o.ArgsAppend) > 0 {
		args := make([]string, 0, len(o.ArgsPrepend)+len(ac.Args)+len(o.ArgsAppend))
		args = append(args, o.ArgsPrepend...)
		args = append(args, ac.Args...)
		ac.Args = append(args, o.ArgsAppend...)
	}
	for k, v := range o.Env {
		ac.Env = setMapKey(ac.Env, k, v)
	}
	ac.Context = side[:len(side)-1]
	return ac
}

// AttrSource returns where an agent attribute's effective value comes
// from: the context block ForContext applied when it sets the attribute,
// else the attribute itself.
func (c *Config) AttrSource(ac *AgentConfig, attr string) (Contribution, bool) {
	key := "agent." + ac.Name + "."
	if ac.Context != "" {
		if src, ok := c.Source(key + ac.Context + "." + attr); ok {
			return src, true
		}
	}
	return c.Source(key + attr)
}

// AgentForRun returns the agent a run uses: ac with the block for the
// run's context applied, then the profile's pins (model, effort,
// max_budget_usd), which win over the agent's own. prof may be nil.
func (c *Config) AgentForRun(prof *ProfileConfig, ac *AgentConfig, inside bool) AgentConfig {
	out := c.ForContext(*ac, inside)
	if prof == nil {
		return out
	}
	if prof.Model != "" {
		out.Model = prof.Model
	}
	if prof.Effort != "" {
		out.Effort = prof.Effort
	}
	if prof.MaxBudgetUSD != "" {
		out.MaxBudgetUSD = prof.MaxBudgetUSD
	}
	return out
}
