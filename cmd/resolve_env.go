package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/root"
)

// resolveEnv calls root.Resolve and finalises env.WorkDir according to the
// project-aware policy in applyWorkDirFlag. Every cmd that requires a project
// context should use this instead of root.Resolve directly so downstream
// consumers (sandbox grants, container mounts, prompt context, git metadata)
// read coherent values from env.
func resolveEnv() (*root.ResolvedEnv, error) {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return nil, err
	}
	// root.Resolve returns a project-less env when only --org resolves
	// (scratch mode for exec/parallel). Commands using resolveEnv strictly
	// require a project — surface the missing .ateam/ here so callers can
	// rely on env.ProjectDir / env.Config being populated.
	if env.ProjectDir == "" {
		return nil, fmt.Errorf("no .ateam/ found — this command requires a project context; run 'ateam init' first")
	}
	return applyWorkDirFlag(env)
}

// lookupEnv calls root.Lookup and applies the work-dir policy. Use this when
// a project is optional (exec/parallel can run org-less).
func lookupEnv() (*root.ResolvedEnv, error) {
	env, err := root.Lookup(orgFlag, projectFlag)
	if err != nil {
		return nil, err
	}
	return applyWorkDirFlag(env)
}

// applyWorkDirFlag finalises env.WorkDir using this precedence:
//
//  1. Explicit --work-dir wins.
//  2. Otherwise, when cwd is inside the project tree, the agent runs from
//     the project root (git-style). This lets `cd subdir && ateam report`
//     operate on the whole project, matching the pre-refactor default.
//  3. When --project points outside cwd's tree, the agent runs in cwd (the
//     user is driving the project's state from elsewhere but hasn't moved
//     into it). report/code/review/verify/all will then fail PreRunE
//     unless cwd is itself a git repo.
func applyWorkDirFlag(env *root.ResolvedEnv) (*root.ResolvedEnv, error) {
	if workDirFlag != "" {
		if err := env.OverrideWorkDir(workDirFlag); err != nil {
			return nil, err
		}
		return env, nil
	}
	if env.ProjectDir == "" {
		return env, nil
	}
	projectRoot := filepath.Dir(env.ProjectDir)
	if pathInside(env.WorkDir, projectRoot) {
		if err := env.OverrideWorkDir(projectRoot); err != nil {
			return nil, err
		}
	}
	return env, nil
}

// pathInside reports whether child is the same path as parent or a descendant.
// Both should be absolute; returns false on any resolution error (treat as
// outside so the caller keeps cwd).
func pathInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
