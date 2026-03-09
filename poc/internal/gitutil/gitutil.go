package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type ProjectMeta struct {
	CommitHash    string
	CommitDate    string
	CommitMessage string
	Uncommitted   []string
}

func GetProjectMeta(dir string) (*ProjectMeta, error) {
	logCmd := exec.Command("git", "log", "-1", "--format=%H%n%aI%n%s")
	logCmd.Dir = dir
	logOut, err := logCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	lines := strings.SplitN(strings.TrimSpace(string(logOut)), "\n", 3)
	if len(lines) < 3 {
		return nil, fmt.Errorf("unexpected git log output")
	}

	commitDate := lines[1]
	if t, err := time.Parse(time.RFC3339, lines[1]); err == nil {
		commitDate = t.Format("2006-01-02 15:04")
	}

	meta := &ProjectMeta{
		CommitHash:    lines[0],
		CommitDate:    commitDate,
		CommitMessage: lines[2],
	}

	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusOut, err := statusCmd.Output()
	if err != nil {
		return meta, nil
	}

	for _, l := range strings.Split(strings.TrimSpace(string(statusOut)), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			meta.Uncommitted = append(meta.Uncommitted, l)
		}
	}

	return meta, nil
}
