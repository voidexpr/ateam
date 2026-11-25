package sandboxprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// dockerInfo is what the daemon's /info says that tells how much a caller
// of the daemon can do.
type dockerInfo struct {
	Name            string
	OperatingSystem string
	DefaultRuntime  string
	SecurityOptions []string
}

// checkDocker asks the first Docker socket that answers for /info. It
// starts nothing. forced is set when the answer decides the verdict.
func checkDocker(sockets []string) (perm Permission, detail string, forced Verdict) {
	for _, socket := range sockets {
		info, err := queryDocker(socket)
		if err != nil {
			continue
		}
		detail, limited := describeDocker(info)
		if limited {
			return Present, detail, OK
		}
		return Present, detail + "; cannot tell if limited: --with docker-build", Risk
	}
	return Absent, "no daemon answers", ""
}

func queryDocker(socket string) (dockerInfo, error) {
	var info dockerInfo
	ctx, cancel := context.WithTimeout(context.Background(), netTimeout)
	defer cancel()
	client := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/info", nil)
	if err != nil {
		return info, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("docker /info: %s", resp.Status)
	}
	return info, json.NewDecoder(resp.Body).Decode(&info)
}

// describeDocker summarises a daemon. limited is true when the daemon
// itself confines its containers: rootless, or sysbox as the default
// runtime.
func describeDocker(info dockerInfo) (detail string, limited bool) {
	parts := []string{"rootful"}
	for _, option := range info.SecurityOptions {
		switch {
		case strings.Contains(option, "name=rootless"):
			parts[0], limited = "rootless", true
		case strings.Contains(option, "name=userns"):
			parts = append(parts, "user namespaces")
		}
	}
	if strings.Contains(info.DefaultRuntime, "sysbox") {
		limited = true
	}
	if info.OperatingSystem != "" {
		parts = append(parts, info.OperatingSystem)
	}
	if info.Name != "" {
		parts = append(parts, "host "+info.Name)
	}
	if info.DefaultRuntime != "" {
		parts = append(parts, "runtime "+info.DefaultRuntime)
	}
	return strings.Join(parts, ", "), limited
}

func checkRoot() (Permission, string) {
	if os.Geteuid() == 0 {
		return Present, "uid 0"
	}
	return Absent, "uid " + strconv.Itoa(os.Geteuid())
}

func checkGroup(name string) (Permission, string) {
	ids, err := os.Getgroups()
	if err != nil {
		return Absent, ""
	}
	for _, id := range ids {
		if group, err := user.LookupGroupId(strconv.Itoa(id)); err == nil && group.Name == name {
			return Present, ""
		}
	}
	return Absent, ""
}

// capNames are the Linux capabilities by bit, as in linux/capability.h.
var capNames = []string{
	"chown", "dac_override", "dac_read_search", "fowner", "fsetid", "kill", "setgid", "setuid", "setpcap",
	"linux_immutable", "net_bind_service", "net_broadcast", "net_admin", "net_raw", "ipc_lock", "ipc_owner",
	"sys_module", "sys_rawio", "sys_chroot", "sys_ptrace", "sys_pacct", "sys_admin", "sys_boot", "sys_nice",
	"sys_resource", "sys_time", "sys_tty_config", "mknod", "lease", "audit_write", "audit_control", "setfcap",
	"mac_override", "mac_admin", "syslog", "wake_alarm", "block_suspend", "audit_read", "perfmon", "bpf",
	"checkpoint_restore",
}

// checkCaps reads the effective capabilities from a /proc/<pid>/status
// file.
func checkCaps(statusFile string) (Permission, string) {
	f, err := os.Open(statusFile)
	if err != nil {
		return Absent, "cannot read " + statusFile
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if hex, ok := strings.CutPrefix(scanner.Text(), "CapEff:"); ok {
			if names := decodeCaps(strings.TrimSpace(hex)); len(names) > 0 {
				return Present, strings.Join(names, " ")
			}
			return Absent, ""
		}
	}
	return Absent, ""
}

func decodeCaps(hex string) []string {
	mask, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return nil
	}
	var names []string
	for bit, name := range capNames {
		if mask&(1<<bit) != 0 {
			names = append(names, name)
		}
	}
	return names
}

// dirCreatable remembers, per folder, whether it accepts a new entry: many
// programs share a folder, one marker file is enough.
var dirCreatable sync.Map

// checkProgramFile reports whether a program's file can be replaced: the
// file is writable, or its folder accepts a new entry to rename over it.
func checkProgramFile(path string) (Permission, string) {
	perm, detail := CheckFile(path, nil)
	if perm == Write {
		return Write, joinDetail(detail, "the file is writable")
	}
	dir := filepath.Dir(path)
	creatable, known := dirCreatable.Load(dir)
	if !known {
		creatable = canCreateIn(dir)
		dirCreatable.Store(dir, creatable)
	}
	if creatable.(bool) {
		return Create, joinDetail(detail, "its folder accepts a new file")
	}
	return perm, detail
}

// Mount is one line of /proc/self/mountinfo.
type Mount struct {
	Root     string // the path inside the source filesystem
	Point    string
	FSType   string
	ReadOnly bool
}

// kernelFS are filesystems the kernel or the container runtime provides:
// not something the host shares.
var kernelFS = []string{
	"proc", "sysfs", "devpts", "tmpfs", "devtmpfs", "mqueue", "cgroup", "cgroup2", "overlay", "shm",
	"securityfs", "debugfs", "tracefs", "bpf", "pstore", "configfs", "fusectl", "hugetlbfs", "binfmt_misc",
	"autofs", "nsfs", "ramfs", "squashfs", "efivarfs",
}

// runtimeFiles are the files a container runtime mounts for name
// resolution.
var runtimeFiles = []string{"/etc/resolv.conf", "/etc/hostname", "/etc/hosts"}

// ParseMounts returns the mounts of a mountinfo text that come from
// outside: not the root, not a kernel filesystem, not the runtime's own
// files.
func ParseMounts(text string) []Mount {
	var mounts []Mount
	for _, line := range strings.Split(text, "\n") {
		// ID parent major:minor root point options [optional...] - fstype source superoptions
		before, after, ok := strings.Cut(line, " - ")
		head, tail := strings.Fields(before), strings.Fields(after)
		if !ok || len(head) < 6 || len(tail) < 1 {
			continue
		}
		mount := Mount{Root: unescapeMount(head[3]), Point: unescapeMount(head[4]), FSType: tail[0],
			ReadOnly: slices.Contains(strings.Split(head[5], ","), "ro")}
		if mount.Point == "/" || slices.Contains(kernelFS, mount.FSType) || slices.Contains(runtimeFiles, mount.Point) {
			continue
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

// unescapeMount decodes the octal escapes of mountinfo (a space is \040).
func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if code, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(code))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
