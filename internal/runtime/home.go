package runtime

import (
	"os"
	"path/filepath"
)

// ConfigFile is the name of the configuration file in every layer.
const ConfigFile = "ateam.hcl"

// DirName is the name of the per-project and per-user ateam directory.
const DirName = ".ateam"

// HomeEnv overrides the per-user ateam directory (tests, containers).
const HomeEnv = "ATEAM_HOME"

// HomeDir returns the per-user ateam directory: $ATEAM_HOME, else
// $HOME/.ateam. It is the lowest user-controlled config layer and holds
// the user's state (scratch runs, registry, caches, agent config dirs).
// Empty only when no home directory can be determined.
func HomeDir() string {
	if d := os.Getenv(HomeEnv); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			return abs
		}
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, DirName)
}

// StateSubdir is the directory under HomeDir that holds user state as
// opposed to configuration: scratch-mode state.sqlite and logs, the
// project registry, caches, agent config dirs.
const StateSubdir = "state"
