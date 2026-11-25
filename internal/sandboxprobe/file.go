package sandboxprobe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/sandboxpolicy"
)

const netTimeout = 5 * time.Second

// markerPrefix starts the name of the only files the checks create.
const markerPrefix = ".ateam-probe-"

// CheckFile reports what can be done with path, without changing it: a
// file is opened and never written, a folder gets one empty marker file
// that is removed at once. before is what was seen of the path from
// outside a sandbox, nil when the check runs where it was built.
func CheckFile(path string, before *Facts) (Permission, string) {
	link := ""
	if real := sandboxpolicy.RealPath(path); real != path {
		link = "-> " + display.TildeHome(real)
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		if before != nil && before.Exists {
			return Blocked, joinDetail(link, "hidden: it exists outside")
		}
		switch dir := existingParent(path); {
		case dir == "":
		case dir == filepath.Dir(path) && liveSidecar(path):
			return Absent, joinDetail(link, "creation not tried: a running program would trust a file here")
		case dir == filepath.Dir(path):
			// The path itself is tried: a sandbox refuses by name, and the
			// folder above may accept any other entry.
			if createAndRemove(path) {
				return Create, link
			}
		case canCreateIn(dir):
			return Create, joinDetail(link, "from "+display.TildeHome(dir))
		}
		return Absent, link
	case err != nil:
		return Blocked, link
	case info.IsDir():
		perm, detail := checkDir(path, before)
		return perm, joinDetail(link, detail)
	case info.Mode()&fs.ModeSocket != 0:
		conn, err := net.DialTimeout("unix", path, netTimeout)
		if err != nil {
			return Blocked, joinDetail(link, "socket")
		}
		_ = conn.Close()
		return Connect, joinDetail(link, "socket")
	case !info.Mode().IsRegular():
		// Opening a pipe or a device can block or have an effect.
		return Read, joinDetail(link, "not a regular file: not opened")
	}
	perm, detail := checkRegular(path, before)
	return perm, joinDetail(link, detail)
}

func checkDir(path string, before *Facts) (Permission, string) {
	entries, err := os.ReadDir(path)
	readable := err == nil
	hidden := readable && len(entries) == 0 && before != nil && before.IsDir && !before.Empty
	if hidden {
		readable = false
	}
	switch writable := canCreateIn(path); {
	case writable && readable:
		return Write, ""
	case writable && hidden:
		return Write, "listed empty: it has entries outside"
	case writable:
		return Write, "not readable"
	case hidden:
		return Blocked, "listed empty: it has entries outside"
	case readable:
		return Read, ""
	}
	return Blocked, ""
}

func checkRegular(path string, before *Facts) (Permission, string) {
	readable, hidden := false, false
	if f, err := os.Open(path); err == nil {
		// One byte, read and dropped: opening alone can succeed on a file
		// whose content is refused.
		_, err := f.Read(make([]byte, 1))
		_ = f.Close()
		readable = err == nil || err == io.EOF
		hidden = err == io.EOF && before != nil && before.Exists && !before.Empty
		if hidden {
			readable = false
		}
	}
	// Append mode without a write: the file keeps its size and its time.
	writable := false
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_ = f.Close()
		writable = true
	}
	switch {
	case writable && readable:
		return Write, ""
	case writable && hidden:
		return Write, "read empty: it has content outside"
	case writable:
		return Write, "not readable"
	case hidden:
		return Blocked, "read empty: it has content outside"
	case readable:
		return Read, ""
	}
	return Blocked, ""
}

// existingParent returns the nearest folder above path that exists, ""
// when what is there is not a folder or cannot be seen.
func existingParent(path string) string {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err == nil {
			if info.IsDir() {
				return dir
			}
			return ""
		}
		if !errors.Is(err, fs.ErrNotExist) || dir == filepath.Dir(dir) {
			return ""
		}
	}
}

// canCreateIn reports whether dir accepts a new entry, by creating an
// empty marker file and removing it.
func canCreateIn(dir string) bool {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	return createAndRemove(filepath.Join(dir, fmt.Sprintf("%s%d-%s", markerPrefix, os.Getpid(), hex.EncodeToString(suffix))))
}

// createAndRemove reports whether an empty file can be created at path,
// where nothing is, and removes it at once. A file that cannot be removed
// is reported: it is the one trace a check can leave.
func createAndRemove(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	_ = f.Close()
	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "ateam probe: an empty file created to test %s could not be removed: %v\n", path, err)
	}
	return true
}

// liveSidecar reports whether path is a file a running program creates
// beside its data and trusts when it finds it (SQLite's journal, a lock).
// Creating one, even for an instant, could be taken for the real thing,
// and its folder says nothing of a sandbox that refuses by name: whether
// it can be created is left unknown.
func liveSidecar(path string) bool {
	name := filepath.Base(path)
	for _, suffix := range []string{"-wal", "-shm", "-journal", ".lock", ".pid"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// Observe records what can be seen of path from here.
func Observe(path string) *Facts {
	info, err := os.Stat(path)
	if err != nil {
		return &Facts{}
	}
	facts := &Facts{Exists: true, IsDir: info.IsDir()}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		facts.Empty = err != nil || len(entries) == 0
	} else {
		facts.Empty = info.Size() == 0
	}
	return facts
}
