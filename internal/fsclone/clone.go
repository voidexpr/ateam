// Package fsclone provides a cross-platform Clone helper that copies a file
// using the OS's copy-on-write facilities when available (clonefile(2) on
// Darwin/APFS, ioctl FICLONE on Linux btrfs/xfs/zfs) and falls back to a
// regular byte copy otherwise.
//
// On supported filesystems the destination shares blocks with the source, so
// post-run promote of runtime/<exec_id>/ files into roles/<id>/ does not
// double disk usage. On unsupported filesystems the call still succeeds; it
// just consumes the expected amount of space.
package fsclone

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Clone copies src to dst, preferring a CoW clone. dst's parent directory
// must exist. If dst already exists it is replaced.
func Clone(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	// Remove a pre-existing destination so cp -c (which requires the
	// destination to not exist on Darwin) and the byte-copy fallback both
	// behave predictably.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", dst, err)
	}

	if err := tryCloneCmd(src, dst); err == nil {
		return nil
	}
	return byteCopy(src, dst)
}

func tryCloneCmd(src, dst string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("cp", "-pc", src, dst)
	case "linux":
		cmd = exec.Command("cp", "-p", "--reflink=auto", src, dst)
	default:
		return fmt.Errorf("unsupported platform %q", runtime.GOOS)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cp clone failed: %w (%s)", err, out)
	}
	return nil
}

func byteCopy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dst, err)
	}
	return nil
}
