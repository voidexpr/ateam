package cmd

import "github.com/ateam/internal/root"

// resolveEnv calls root.Resolve and immediately applies the persistent
// --work-dir override. Every cmd that requires a project context should use
// this instead of root.Resolve directly so downstream consumers (sandbox
// grants, container mounts, prompt context, git metadata) read coherent
// values from env.
func resolveEnv() (*root.ResolvedEnv, error) {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return nil, err
	}
	return applyWorkDirFlag(env)
}

// lookupEnv calls root.Lookup and applies --work-dir. Use this when a project
// is optional (exec/parallel can run org-less).
func lookupEnv() (*root.ResolvedEnv, error) {
	env, err := root.Lookup(orgFlag, projectFlag)
	if err != nil {
		return nil, err
	}
	return applyWorkDirFlag(env)
}

func applyWorkDirFlag(env *root.ResolvedEnv) (*root.ResolvedEnv, error) {
	if workDirFlag == "" {
		return env, nil
	}
	if err := env.OverrideWorkDir(workDirFlag); err != nil {
		return nil, err
	}
	return env, nil
}
