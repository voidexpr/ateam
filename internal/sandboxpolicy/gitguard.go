package sandboxpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// GitGuard records the files of a repository that run or steer commands
// outside a sandbox later: .git/config and everything in .git/hooks. No
// sandbox backend can keep them read-only inside a writable working
// folder, so a launch compares them after the command ends.
type GitGuard struct {
	dir   string            // the working folder
	files map[string]string // path relative to dir -> content hash
}

// guarded lists the paths of a working folder a GitGuard watches.
var guarded = []string{filepath.Join(".git", "config"), filepath.Join(".git", "hooks")}

// SnapshotGit records the guarded files of workDir. ok is false when
// workDir holds no .git folder.
func SnapshotGit(workDir string) (g GitGuard, ok bool) {
	if info, err := os.Stat(filepath.Join(workDir, ".git")); err != nil || !info.IsDir() {
		return GitGuard{}, false
	}
	return GitGuard{dir: workDir, files: hashGuarded(workDir)}, true
}

func hashGuarded(workDir string) map[string]string {
	files := map[string]string{}
	for _, rel := range guarded {
		_ = filepath.WalkDir(filepath.Join(workDir, rel), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			sum := sha256.Sum256(data)
			name, _ := filepath.Rel(workDir, path)
			files[name] = hex.EncodeToString(sum[:])
			return nil
		})
	}
	return files
}

// Backup copies the guarded files into dest, so a change can be undone.
func (g GitGuard) Backup(dest string) error {
	for name := range g.files {
		data, err := os.ReadFile(filepath.Join(g.dir, name))
		if err != nil {
			return err
		}
		target := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Changed lists the guarded files added, removed or modified since the
// snapshot, sorted.
func (g GitGuard) Changed() []string {
	now := hashGuarded(g.dir)
	var changed []string
	for name, sum := range now {
		if old, ok := g.files[name]; !ok {
			changed = append(changed, name+" (added)")
		} else if old != sum {
			changed = append(changed, name+" (modified)")
		}
	}
	for name := range g.files {
		if _, ok := now[name]; !ok {
			changed = append(changed, name+" (removed)")
		}
	}
	sort.Strings(changed)
	return changed
}
