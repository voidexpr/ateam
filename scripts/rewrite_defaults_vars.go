//go:build ignore

// One-shot script: walks defaults/prompts/ and applies
// assembler.RewriteContent (the ALL_CAPS → dotted compat-shim mapping)
// to every .md file in place. Run via `go run scripts/rewrite_defaults_vars.go`.
// After commit, the shipped defaults read in the modern vocabulary; the
// engine's compat shim stays for backward-compat with user prompts.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/prompts/assembler"
)

func main() {
	root := "defaults/prompts"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	changed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rewritten := assembler.RewriteContent(string(data))
		if rewritten == string(data) {
			return nil
		}
		if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
			return err
		}
		changed++
		fmt.Printf("rewrote %s\n", path)
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("\n%d files changed.\n", changed)
}
