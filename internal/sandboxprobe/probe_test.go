package sandboxprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ateam/internal/sandboxpolicy"
)

// scratch returns an Env for a scratch home with a working folder in it.
// File permissions do not bind root: the tests that rely on them skip.
func scratch(t *testing.T) Env {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	work := filepath.Join(home, "work")
	mkdir(t, work)
	vars := map[string]string{}
	return Env{GOOS: "linux", Home: home, WorkDir: work, Shell: "/bin/zsh", AteamHome: filepath.Join(home, ".ateam"),
		Getenv: func(name string) string { return vars[name] }}
}

func skipRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not bind root")
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// lock makes dir refuse new entries until the test ends.
func lock(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func noMarkers(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), markerPrefix) {
			t.Errorf("marker file left behind: %s", path)
		}
		return nil
	})
}

func TestCheckFilePermissions(t *testing.T) {
	skipRoot(t)
	env := scratch(t)
	open := filepath.Join(env.Home, "open")
	sealed := filepath.Join(env.Home, "sealed")
	mkdir(t, open)
	write(t, filepath.Join(open, "rw"), "x", 0o644)
	write(t, filepath.Join(open, "ro"), "x", 0o444)
	write(t, filepath.Join(open, "none"), "x", 0o000)
	write(t, filepath.Join(open, "writeonly"), "x", 0o200)
	write(t, filepath.Join(sealed, "ro"), "x", 0o444)
	mkdir(t, filepath.Join(sealed, "closed"))
	lock(t, filepath.Join(sealed, "closed"), 0o000)
	lock(t, sealed, 0o555)

	cases := []struct {
		path   string
		want   Permission
		detail string
	}{
		{filepath.Join(open, "rw"), Write, ""},
		{filepath.Join(open, "ro"), Read, ""},
		{filepath.Join(open, "none"), Blocked, ""},
		{filepath.Join(open, "writeonly"), Write, "not readable"},
		{open, Write, ""},
		{sealed, Read, ""},
		{filepath.Join(sealed, "closed"), Blocked, ""},
		{filepath.Join(open, "missing"), Create, ""},
		{filepath.Join(open, "missing", "deeper"), Create, "from ~/open"},
		{filepath.Join(sealed, "missing"), Absent, ""},
		{filepath.Join(open, "rw", "below-a-file"), Absent, ""},
	}
	for _, c := range cases {
		got, detail := CheckFile(c.path, nil)
		if got != c.want || detail != c.detail {
			t.Errorf("CheckFile(%s) = %s %q, want %s %q", strings.TrimPrefix(c.path, env.Home), got, detail, c.want, c.detail)
		}
	}
	noMarkers(t, env.Home)
}

// The write check must leave a file as it was: an earlier probe appended a
// line to .ateam/ateam.hcl.
func TestCheckFileChangesNothing(t *testing.T) {
	env := scratch(t)
	file := filepath.Join(env.WorkDir, "ateam.hcl")
	write(t, file, "profile \"x\" {}\n", 0o644)
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	for _, path := range []string{file, env.WorkDir} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if perm, _ := CheckFile(file, nil); perm != Write {
		t.Fatalf("file = %s, want write", perm)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len("profile \"x\" {}\n")) || !info.ModTime().Equal(old) {
		t.Errorf("the write check changed the file: size %d, time %s", info.Size(), info.ModTime())
	}
	if data, _ := os.ReadFile(file); string(data) != "profile \"x\" {}\n" {
		t.Errorf("content changed: %q", data)
	}
	if perm, _ := CheckFile(env.WorkDir, nil); perm != Write {
		t.Fatalf("folder = %s, want write", perm)
	}
	entries, _ := os.ReadDir(env.WorkDir)
	if len(entries) != 1 {
		t.Errorf("the folder check left entries: %v", entries)
	}
	noMarkers(t, env.Home)
}

// A path with nothing there is tried by its own name, and nothing stays.
// A file a running program would trust is not tried at all.
func TestCheckFileCreateLeavesNothing(t *testing.T) {
	env := scratch(t)
	for name, want := range map[string]Permission{"profiles": Create, "state.sqlite-wal": Absent} {
		path := filepath.Join(env.WorkDir, name)
		if perm, _ := CheckFile(path, nil); perm != want {
			t.Errorf("%s = %s, want %s", name, perm, want)
		}
		if _, err := os.Lstat(path); err == nil {
			t.Errorf("%s was left behind", name)
		}
	}
	if entries, _ := os.ReadDir(env.WorkDir); len(entries) != 0 {
		t.Errorf("entries left: %v", entries)
	}
	if !liveSidecar("/x/state.sqlite-shm") || liveSidecar("/x/profiles") {
		t.Error("liveSidecar")
	}
}

func TestCheckFileLinkToClosedTarget(t *testing.T) {
	skipRoot(t)
	env := scratch(t)
	target := filepath.Join(env.Home, "vault", "keys")
	write(t, filepath.Join(target, "id"), "x", 0o600)
	link := filepath.Join(env.Home, ".ssh")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	lock(t, target, 0o000)
	perm, detail := CheckFile(link, nil)
	if perm != Blocked || detail != "-> ~/vault/keys" {
		t.Errorf("link to a closed folder = %s %q, want blocked and its target", perm, detail)
	}
}

// A sandbox that mounts an empty folder over an excluded one, or hides a
// path, is told apart by what was seen before the launch.
func TestCheckFileHiddenByBeforeFacts(t *testing.T) {
	env := scratch(t)
	dir := filepath.Join(env.Home, "private")
	mkdir(t, dir)
	empty := filepath.Join(env.Home, "token")
	write(t, empty, "", 0o644)
	lock(t, env.Home, 0o555)
	lock(t, dir, 0o555)
	if err := os.Chmod(empty, 0o444); err != nil {
		t.Fatal(err)
	}
	hadEntries := &Facts{Exists: true, IsDir: true}
	if perm, detail := CheckFile(dir, hadEntries); perm != Blocked || !strings.Contains(detail, "listed empty") {
		t.Errorf("folder seen empty = %s %q, want blocked", perm, detail)
	}
	if perm, _ := CheckFile(dir, &Facts{Exists: true, IsDir: true, Empty: true}); perm != Read {
		t.Errorf("folder that was empty outside = %s, want read", perm)
	}
	if perm, detail := CheckFile(empty, &Facts{Exists: true}); perm != Blocked || !strings.Contains(detail, "read empty") {
		t.Errorf("file read empty = %s %q, want blocked", perm, detail)
	}
	if perm, detail := CheckFile(filepath.Join(dir, "gone"), &Facts{Exists: true}); perm != Blocked || !strings.Contains(detail, "hidden") {
		t.Errorf("path that exists outside only = %s %q, want blocked", perm, detail)
	}
}

func TestObserve(t *testing.T) {
	env := scratch(t)
	write(t, filepath.Join(env.WorkDir, "f"), "x", 0o644)
	if got := Observe(env.WorkDir); !got.Exists || !got.IsDir || got.Empty {
		t.Errorf("folder with an entry = %+v", got)
	}
	if got := Observe(filepath.Join(env.WorkDir, "f")); !got.Exists || got.IsDir || got.Empty {
		t.Errorf("file with content = %+v", got)
	}
	if got := Observe(filepath.Join(env.WorkDir, "missing")); got.Exists {
		t.Errorf("missing = %+v", got)
	}
}

func TestCheckFileSocket(t *testing.T) {
	// A socket path is limited to about 100 bytes: not under t.TempDir().
	dir, err := os.MkdirTemp("/tmp", "ap")
	if err != nil {
		t.Skip("cannot create a short folder for a socket:", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skip("cannot listen on a Unix socket here:", err)
	}
	if perm, _ := CheckFile(path, nil); perm != Connect {
		t.Errorf("listening socket = %s, want connect", perm)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	if perm, _ := CheckFile(path, nil); perm != Blocked {
		t.Errorf("socket nobody listens on = %s, want blocked", perm)
	}
}

func TestPrograms(t *testing.T) {
	skipRoot(t)
	env := scratch(t)
	early := filepath.Join(env.Home, "early")
	system := filepath.Join(env.Home, "system")
	mkdir(t, early)
	write(t, filepath.Join(system, "git"), "#!/bin/sh\n", 0o555)
	write(t, filepath.Join(system, "loose"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(system, "notes.txt"), "x", 0o644)
	lock(t, system, 0o555)
	env.Path = []string{early, system}
	env.Shell = ""

	checks := programChecks(env, []string{"loose", "notes.txt"})
	results := map[string]Result{}
	for _, r := range Run(env, checks) {
		results[r.ID] = r
	}
	want := map[string]struct {
		perm    Permission
		verdict Verdict
	}{
		"programs:git":           {Present, OK},
		"programs:git:file":      {Read, OK},
		"programs:loose:file":    {Write, Risk},
		"programs:sudo":          {Absent, OK},
		"programs:notes.txt":     {Absent, OK},
		"programs:PATH:~/early":  {Write, Risk},
		"programs:PATH:~/system": {Read, OK},
	}
	for id, w := range want {
		got, ok := results[id]
		if !ok || got.Permission != w.perm || got.Verdict != w.verdict {
			t.Errorf("%s = %s %s (found %v), want %s %s", id, got.Permission, got.Verdict, ok, w.perm, w.verdict)
		}
	}
	if _, ok := results["programs:sudo:file"]; ok {
		t.Error("a program that is not found has no file to check")
	}
	if detail := results["programs:PATH:~/early"].Detail; !strings.Contains(detail, "runs instead of: git, loose") {
		t.Errorf("a writable folder earlier in PATH must name what it shadows: %q", detail)
	}

	// A file that cannot be written in a folder that accepts new entries
	// can still be replaced: renamed over.
	dirCreatable.Clear()
	if err := os.Chmod(system, 0o755); err != nil {
		t.Fatal(err)
	}
	if perm, detail := checkProgramFile(filepath.Join(system, "git")); perm != Create || !strings.Contains(detail, "folder") {
		t.Errorf("read-only program in a writable folder = %s %q, want create", perm, detail)
	}
	noMarkers(t, env.Home)
}

func TestCheckNetLocal(t *testing.T) {
	if perm, _ := CheckNet(MethodListen, "127.0.0.1:0"); perm != Listen {
		t.Skipf("cannot listen on the loopback here: %s", perm)
	}
	if perm, _ := CheckNet(MethodLoopback, ""); perm != Connect {
		t.Errorf("own loopback = %s, want connect", perm)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if perm, _ := CheckNet(MethodDial, addr); perm != Connect {
		t.Errorf("dial a listening port = %s, want connect", perm)
	}
	if perm, _ := CheckNet(MethodListen, addr); perm != Blocked {
		t.Errorf("listen on a taken port = %s, want blocked", perm)
	}
	_ = ln.Close()
	if perm, _ := CheckNet(MethodDial, addr); perm != Blocked {
		t.Errorf("dial a closed port = %s, want blocked", perm)
	}
}

// A URL is reached with one HEAD and nothing of the user's: no cookie, no
// credential.
func TestCheckNetURLSendsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot listen on the loopback here:", err)
	}
	seen := make(chan *http.Request, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r
		http.Redirect(w, r, "http://127.0.0.1:1/elsewhere", http.StatusFound)
	})}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })
	for _, name := range proxyVars {
		t.Setenv(name, "")
	}
	t.Setenv("NO_PROXY", "*")
	if perm, _ := CheckNet(MethodURLDirect, "http://"+ln.Addr().String()+"/"); perm != Connect {
		t.Fatalf("direct = %s, want connect: a redirect is an answer and is not followed", perm)
	}
	req := <-seen
	if req.Method != http.MethodHead || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" {
		t.Errorf("request = %s, headers %v", req.Method, req.Header)
	}
	if perm, detail := CheckNet(MethodURLProxy, "http://"+ln.Addr().String()+"/"); perm != Connect || !strings.Contains(detail, "no proxy") {
		t.Errorf("without a proxy variable = %s %q", perm, detail)
	}
	<-seen
}

func TestDefaultsForScratchHome(t *testing.T) {
	env := scratch(t)
	env.Secrets = []string{filepath.Join(env.Home, ".ssh"), filepath.Join(env.Home, ".aws")}
	hooks := filepath.Join(env.WorkDir, ".git", "hooks")
	write(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(hooks, "pre-push.sample"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(env.WorkDir, ".git", "config"), "[core]\n\tbare = false\n", 0o644)

	checks := Defaults(env, nil)
	ids := map[string]Check{}
	for _, c := range checks {
		if _, dup := ids[c.ID]; dup {
			t.Errorf("id used twice: %s", c.ID)
		}
		ids[c.ID] = c
		if !slices.Contains(Groups, c.Group) {
			t.Errorf("%s: group %q is not in Groups", c.ID, c.Group)
		}
	}
	for _, id := range []string{
		"where:./", "where:~", "secrets:~/.ssh", "shell:~/.zshrc", "agent:~/.claude/settings.json", "agent:.mcp.json",
		"ateam:~/.ateam/profiles", "ateam:.ateam/profiles", "git:.git/config", "git:.git/hooks", "git:.git/hooks/pre-commit",
		"git:~/.gitconfig", "programs:ateam", "programs:git", "sockets:/var/run/docker.sock", "docker:docker daemon",
		"playwright:playwright-cli", "identity:root", "identity:linux capabilities", "network:own loopback",
		"network:https://example.com (proxy)", "network:https://example.com (direct)", "network:169.254.169.254:80",
	} {
		if _, ok := ids[id]; !ok {
			t.Errorf("missing built-in check %s", id)
		}
	}
	if _, ok := ids["git:.git/hooks/pre-push.sample"]; ok {
		t.Error("a .sample hook is not a hook")
	}
	if c := ids["secrets:~/.ssh"]; !slices.Equal(c.Safe, closed) {
		t.Errorf("a secret is safe only when closed: %v", c.Safe)
	}
	if c := ids["where:./"]; c.Safe != nil {
		t.Errorf("the working folder is reported, not judged: %v", c.Safe)
	}
}

func TestGitHooksPathAndWorktree(t *testing.T) {
	env := scratch(t)
	repo := filepath.Join(env.Home, "repo")
	write(t, filepath.Join(repo, ".git", "config"), "[core]\n\thooksPath = ~/shared-hooks\n", 0o644)
	write(t, filepath.Join(env.Home, "shared-hooks", "commit-msg"), "#!/bin/sh\n", 0o755)
	gitDir := filepath.Join(repo, ".git", "worktrees", "wt")
	write(t, filepath.Join(gitDir, "commondir"), "../..\n", 0o644)
	write(t, filepath.Join(env.WorkDir, ".git"), "gitdir: "+gitDir+"\n", 0o644)

	var names []string
	for _, c := range gitChecks(env) {
		names = append(names, c.Name)
	}
	for _, want := range []string{"~/repo/.git/config", "~/shared-hooks", "~/shared-hooks/commit-msg"} {
		if !slices.Contains(names, want) {
			t.Errorf("git checks of a worktree with core.hooksPath miss %s: %v", want, names)
		}
	}
}

func TestDiscover(t *testing.T) {
	env := scratch(t)
	for _, file := range []string{
		".ssh/id_rsa", "id_ed25519", "id_ed25519.pub", "server.pem", ".env", ".netrc", ".config/app/api-token",
		".config/app/deep/down/secret.key", "notes.txt", "work/.env.local", "work/.env.example", "work/src/token.go",
		"work/a/b/deploy.key", "work/a/b/c/too-deep.key", "work/node_modules/pkg/private.key", "work/.git/credentials",
		"other/.git/HEAD", "plain/readme",
	} {
		write(t, filepath.Join(env.Home, file), "x", 0o600)
	}
	env.Names = []string{"GH_TOKEN", "AWS_SECRET_ACCESS_KEY", "DB_PASSWORD", "KEYBOARD_LAYOUT", "GIT_CONFIG_KEY_0", "PATH", "DATABASE_URL"}
	values := map[string]string{"GH_TOKEN": "not-to-be-seen"}
	env.Getenv = func(name string) string { return values[name] }

	checks := Discover(env, []string{filepath.Join(env.Home, ".ssh"), filepath.Join(env.Home, ".netrc")})
	var names []string
	for _, c := range checks {
		names = append(names, c.Group+" "+c.Name)
		if data, _ := json.Marshal(c); bytes.Contains(data, []byte("not-to-be-seen")) {
			t.Errorf("a check carries a variable's value: %s", data)
		}
	}
	want := []string{
		"secrets ~/.env", "secrets ~/id_ed25519", "secrets ~/server.pem", "secrets ~/.config/app/api-token",
		"secrets .env.local", "secrets a/b/deploy.key",
		"secrets AWS_SECRET_ACCESS_KEY", "secrets DATABASE_URL", "secrets DB_PASSWORD", "secrets GH_TOKEN",
		"where ~/other",
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("discovered:\n  %s\nwant:\n  %s", strings.Join(names, "\n  "), strings.Join(want, "\n  "))
	}
	results := Run(env, checks)
	for _, r := range results {
		if r.Name == "GH_TOKEN" && (r.Permission != Set || r.Verdict != Risk) {
			t.Errorf("a set token = %s %s, want set RISK", r.Permission, r.Verdict)
		}
		if r.Name == "DB_PASSWORD" && r.Permission != Absent {
			t.Errorf("a variable that is not set here = %s, want absent", r.Permission)
		}
		if strings.Contains(r.Detail, "not-to-be-seen") {
			t.Errorf("a result carries a variable's value: %q", r.Detail)
		}
	}
}

func TestDiscoveryCaps(t *testing.T) {
	env := scratch(t)
	for i := range maxRows + 20 {
		write(t, filepath.Join(env.Home, fmt.Sprintf("k%03d.pem", i)), "x", 0o600)
	}
	if got := len(Discover(env, nil)); got != maxRows {
		t.Errorf("rows = %d, want the cap %d", got, maxRows)
	}
}

const mountinfoSample = `1470 1359 0:230 / / rw,relatime master:580 - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l
1471 1470 0:233 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
1483 1470 0:48 /Users/nicolas/proj /workspace rw,relatime - fakeowner /run/host_mark/Users rw,fakeowner
1484 1470 254:1 /home/nicolas /home/agent/host\040home rw,relatime - ext4 /dev/vda1 rw
1485 1470 254:1 /docker/containers/abc/resolv.conf /etc/resolv.conf rw,relatime - ext4 /dev/vda1 rw
1486 1470 0:24 /docker.sock /var/run/docker.sock rw,nosuid,nodev - tmpfs tmpfs rw
1487 1470 254:1 /srv/data /data ro,relatime - ext4 /dev/vda1 rw
`

func TestMountChecks(t *testing.T) {
	env := scratch(t)
	got := map[string]Check{}
	for _, c := range MountChecks(env, mountinfoSample) {
		got[c.Name] = c
	}
	// A socket is a risk whatever its filesystem; here tmpfs hides it,
	// which the sockets group covers by path.
	want := map[string]struct {
		known Permission
		risk  bool
	}{
		"/workspace":            {Write, false},
		"/home/agent/host home": {Write, true},
		"/data":                 {Read, false},
	}
	if len(got) != len(want) {
		t.Errorf("mounts = %v, want %v", got, want)
	}
	for name, w := range want {
		c, ok := got[name]
		if !ok || c.Known != w.known {
			t.Errorf("%s = %+v, want %s", name, c, w.known)
			continue
		}
		if verdict := judge(c, c.Known); (verdict == Risk) != w.risk {
			t.Errorf("%s: verdict %s, want risk %v", name, verdict, w.risk)
		}
	}
}

func TestDecodeCaps(t *testing.T) {
	if got := decodeCaps("0000000000000000"); len(got) != 0 {
		t.Errorf("no capability = %v", got)
	}
	got := decodeCaps("00000000a80425fb")
	for _, want := range []string{"chown", "net_bind_service", "net_raw", "sys_chroot", "setfcap"} {
		if !slices.Contains(got, want) {
			t.Errorf("Docker's default set misses %s: %v", want, got)
		}
	}
	if slices.Contains(got, "sys_admin") {
		t.Errorf("Docker's default set has no sys_admin: %v", got)
	}
	status := filepath.Join(t.TempDir(), "status")
	write(t, status, "Name:\tsh\nCapEff:\t0000000000200000\n", 0o644)
	if perm, detail := checkCaps(status); perm != Present || detail != "sys_admin" {
		t.Errorf("checkCaps = %s %q, want present sys_admin", perm, detail)
	}
}

// fakeDocker serves an /info answer on a Unix socket.
func fakeDocker(t *testing.T, info string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ap")
	if err != nil {
		t.Skip("cannot create a short folder for a socket:", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skip("cannot listen on a Unix socket here:", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" || r.Method != http.MethodGet {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		fmt.Fprint(w, info)
	})}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close() })
	return path
}

func TestCheckDocker(t *testing.T) {
	cases := []struct {
		name, info string
		verdict    Verdict
		detail     string
	}{
		{"rootful", `{"Name":"colima","OperatingSystem":"Ubuntu 24.04","DefaultRuntime":"runc","SecurityOptions":["name=seccomp,profile=builtin"]}`, Risk, "rootful, Ubuntu 24.04, host colima, runtime runc; cannot tell if limited"},
		{"desktop", `{"Name":"docker-desktop","OperatingSystem":"Docker Desktop","DefaultRuntime":"runc"}`, Risk, "rootful, Docker Desktop"},
		{"rootless", `{"Name":"box","DefaultRuntime":"runc","SecurityOptions":["name=seccomp","name=rootless"]}`, OK, "rootless, host box"},
		{"sysbox", `{"Name":"box","DefaultRuntime":"sysbox-runc","SecurityOptions":["name=userns"]}`, OK, "rootful, user namespaces, host box, runtime sysbox-runc"},
	}
	for _, c := range cases {
		socket := fakeDocker(t, c.info)
		perm, detail, forced := checkDocker([]string{filepath.Join(t.TempDir(), "none.sock"), socket})
		if perm != Present || forced != c.verdict || !strings.HasPrefix(detail, c.detail) {
			t.Errorf("%s = %s %s %q, want present %s %q", c.name, perm, forced, detail, c.verdict, c.detail)
		}
	}
	if perm, _, forced := checkDocker([]string{filepath.Join(t.TempDir(), "none.sock")}); perm != Absent || forced != "" {
		t.Errorf("no daemon = %s %s, want absent", perm, forced)
	}
}

const scriptHeader = "#!/bin/sh\n# id: %s\n# group: docker\n# target: a thing\n# why: because\n# starts: nothing much\n# risk: %s\n%s\n%s\n"

func TestScripts(t *testing.T) {
	env := scratch(t)
	bin := filepath.Join(env.Home, "bin")
	write(t, filepath.Join(bin, "tool"), "#!/bin/sh\n", 0o755)
	env.Path = []string{bin}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	cases := []struct {
		risk, needs, body string
		perm              Permission
		verdict           Verdict
		detail            string
	}{
		{"works", "# needs: tool", "echo first line; echo second; exit 0", Works, Risk, "first line; because"},
		{"works", "", "echo refused; exit 1", Fails, OK, "refused"},
		{"fails", "", "exit 1", Fails, Risk, "because"},
		{"none", "", "exit 0", Works, OK, ""},
		{"works", "", "echo odd; exit 7", Fails, OK, "unknown: exit 7; odd"},
		{"works", "# needs: tool nosuchtool", "exit 0", Absent, OK, "nosuchtool is not in PATH"},
		{"works", "", `test "$ATEAM_PROBE_WORKDIR" = "` + env.WorkDir + `" && test "$PWD" != "$ATEAM_PROBE_WORKDIR"`, Works, Risk, "because"},
	}
	for i, c := range cases {
		script, err := ParseScript(fmt.Sprintf(scriptHeader, "t", c.risk, c.needs, c.body))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		perm, detail, forced := script.Run(env)
		verdict := forced
		if verdict == "" {
			verdict = judge(script.Check(), perm)
		}
		if verdict == Risk {
			detail = joinDetail(detail, script.Why)
		}
		if perm != c.perm || verdict != c.verdict || detail != c.detail {
			t.Errorf("case %d (%s) = %s %s %q, want %s %s %q", i, c.body, perm, verdict, detail, c.perm, c.verdict, c.detail)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(os.TempDir(), scratchPrefix+"*"))
	if len(leftovers) > 0 {
		t.Errorf("scratch folders left behind: %v", leftovers)
	}
}

func TestParseScriptRefusesBadHeaders(t *testing.T) {
	for name, text := range map[string]string{
		"missing field": "# id: x\n# group: docker\necho\n",
		"unknown field": fmt.Sprintf(scriptHeader, "x", "works", "# colour: red", "echo"),
		"bad risk":      fmt.Sprintf(scriptHeader, "x", "maybe", "", "echo"),
	} {
		if _, err := ParseScript(text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	fsys := fstest.MapFS{"probes/other.sh": {Data: []byte(fmt.Sprintf(scriptHeader, "x", "works", "", "echo"))}}
	if _, err := LoadScripts(fsys, "probes"); err == nil {
		t.Error("an id that is not the file's name: accepted")
	}
}

// Every probe script shipped in the binary has a complete header, a known
// group, and says what it starts.
func TestEmbeddedScripts(t *testing.T) {
	scripts, err := Scripts()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range scripts {
		ids = append(ids, s.ID)
		if !slices.Contains(Groups, s.Group) {
			t.Errorf("%s: group %q is not in Groups", s.ID, s.Group)
		}
		if c := s.Check(); !c.Active || c.Starts == "" {
			t.Errorf("%s: not an active check with a starts line: %+v", s.ID, c)
		}
		if !strings.Contains(s.Body, "trap ") {
			t.Errorf("%s: no trap to clean up what it starts", s.ID)
		}
	}
	for _, want := range []string{"docker-build", "docker-build-expand-bind", "playwright-launch"} {
		if !slices.Contains(ids, want) {
			t.Errorf("missing probe script %s: %v", want, ids)
		}
	}
}

func TestSelect(t *testing.T) {
	checks := []Check{
		{ID: "secrets:~/.ssh", Group: "secrets"}, {ID: "secrets:~/.aws", Group: "secrets"},
		{ID: "network:own loopback", Group: "network"}, {ID: "programs:ateam", Group: "programs"},
		{ID: "docker-build", Group: "docker", Active: true},
	}
	ids := func(sel Selection) (string, int) {
		t.Helper()
		selected, skipped, err := Select(checks, sel)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range selected {
			out = append(out, c.ID)
		}
		return strings.Join(out, " "), skipped
	}
	if got, skipped := ids(Selection{}); got != "secrets:~/.ssh secrets:~/.aws network:own loopback programs:ateam" || skipped != 0 {
		t.Errorf("default = %q, %d skipped: an active check is off", got, skipped)
	}
	if got, skipped := ids(Selection{Skip: []string{"network", "programs:ateam"}}); got != "secrets:~/.ssh secrets:~/.aws" || skipped != 2 {
		t.Errorf("skip a group and an id = %q, %d skipped", got, skipped)
	}
	if got, _ := ids(Selection{Only: []string{"secrets:~/.aws", "network"}}); got != "secrets:~/.aws network:own loopback" {
		t.Errorf("only = %q", got)
	}
	if got, _ := ids(Selection{Only: []string{"docker"}}); got != "" {
		t.Errorf("only a group does not turn an active check on: %q", got)
	}
	if got, _ := ids(Selection{Only: []string{"docker-build"}, With: []string{"docker-build"}}); got != "docker-build" {
		t.Errorf("with = %q", got)
	}
	if _, _, err := Select(checks, Selection{Skip: []string{"netwrok"}}); err == nil || !strings.Contains(err.Error(), "secrets") {
		t.Errorf("an unknown name must be refused with the groups: %v", err)
	}
}

func policyFor(t *testing.T, env Env, in sandboxpolicy.Input) *sandboxpolicy.Policy {
	t.Helper()
	in.Home, in.WorkDir, in.Name = env.Home, env.WorkDir, "test"
	p, err := sandboxpolicy.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPolicyExpectationsAndVerdicts(t *testing.T) {
	skipRoot(t)
	env := scratch(t)
	scratchDir := filepath.Join(env.Home, "scratch")
	closedDir := filepath.Join(env.Home, "granted-but-closed")
	mkdir(t, scratchDir)
	mkdir(t, closedDir)
	lock(t, closedDir, 0o555)
	write(t, filepath.Join(env.Home, ".npmrc"), "x", 0o600)
	write(t, filepath.Join(env.Home, ".claude", "settings.json"), "{}", 0o644)
	write(t, filepath.Join(env.Home, ".zshrc"), "", 0o644)

	p := policyFor(t, env, sandboxpolicy.Input{
		Write:   []string{"~/scratch", "~/granted-but-closed", "~/.claude"},
		Exclude: []string{"~/.npmrc", "~/.aws"},
		Domains: []string{"github.com", "*.golang.org"},
		Deny:    []string{"evil.example.com"},
		Listen:  []string{"3000"},
	})
	checks := Cover(Merge(Defaults(env, nil), FromPolicy(env, p)), p)
	byName := map[string]Check{}
	for _, c := range checks {
		byName[c.Group+" "+c.Name] = c
	}
	verdict := func(key string) Verdict {
		t.Helper()
		c, ok := byName[key]
		if !ok {
			t.Fatalf("no check %q", key)
		}
		if c.Type != TypeFile {
			t.Fatalf("%q is not a file check", key)
		}
		perm, _ := CheckFile(c.Target, nil)
		return judge(c, perm)
	}
	for key, want := range map[string]Verdict{
		"where ./":                      OK,    // the working folder is writable, as the policy says
		"profile ~/scratch":             OK,    // a write rule that holds
		"profile ~/granted-but-closed":  Unmet, // a write rule on a folder that refuses writes
		"secrets ~/.npmrc":              Risk,  // excluded, readable
		"secrets ~/.aws":                Risk,  // excluded, can be created
		"agent ~/.claude/settings.json": OK,    // writable through the profile's own grant
		"shell ~/.zshrc":                Risk,  // no rule names it: the built-in judgment stays
	} {
		if got := verdict(key); got != want {
			t.Errorf("%s: verdict %s, want %s (expects %v: %s)", key, got, want, byName[key].Expect, byName[key].ExpectWhy)
		}
	}
	expect := func(key string, want ...Permission) {
		t.Helper()
		c, ok := byName[key]
		if !ok {
			t.Errorf("no check %q", key)
			return
		}
		if !slices.Equal(c.Expect, want) {
			t.Errorf("%s expects %v, want %v", key, c.Expect, want)
		}
	}
	expect("profile https://github.com (proxy)", Connect)
	expect("profile https://evil.example.com (proxy)", Blocked)
	expect("profile https://example.org (proxy)", Blocked)
	expect("profile https://example.org (direct)", Blocked)
	expect("profile 127.0.0.1:3000 (listen)", Listen)
	expect("network 1.1.1.1:443 (direct)", Blocked)
	expect("network https://example.com (proxy)", Blocked)
	expect("network own loopback")
	expect("network DNS example.com")
	for key := range byName {
		if strings.Contains(key, "*") {
			t.Errorf("a wildcard host cannot be dialled: %s", key)
		}
	}

	// A profile without any network rule expects every connection refused.
	closedNet := policyFor(t, env, sandboxpolicy.Input{})
	for _, c := range Cover(networkChecks(), closedNet) {
		judged := c.Method != MethodListen && c.Method != MethodLoopback && c.Method != MethodDNS
		if judged && (!slices.Equal(c.Expect, safe(Blocked)) || !strings.Contains(c.ExpectWhy, "no network rule")) {
			t.Errorf("%s under a closed network expects %v (%s)", c.ID, c.Expect, c.ExpectWhy)
		}
	}
}

func TestJudge(t *testing.T) {
	cases := []struct {
		c    Check
		perm Permission
		want Verdict
	}{
		{Check{}, Write, OK},
		{Check{Safe: closed}, Blocked, OK},
		{Check{Safe: closed}, Read, Risk},
		{Check{Safe: closed}, Create, Risk},
		{Check{Safe: closed, Expect: expectWrite}, Write, OK},
		{Check{Expect: expectWrite}, Read, Unmet},
		{Check{Expect: expectWrite}, Absent, Unmet},
		{Check{Expect: safe(Read, Absent)}, Write, Risk},
		{Check{Expect: safe(Read, Absent)}, Blocked, Unmet},
		{Check{Expect: safe(Connect)}, Blocked, Unmet},
		{Check{Expect: safe(Blocked)}, Connect, Risk},
	}
	for _, c := range cases {
		if got := judge(c.c, c.perm); got != c.want {
			t.Errorf("safe %v expect %v, found %s: %s, want %s", c.c.Safe, c.c.Expect, c.perm, got, c.want)
		}
	}
}

func TestMergeKeepsFirstIDAndLaterExpectation(t *testing.T) {
	env := scratch(t)
	target := filepath.Join(env.Home, "real")
	mkdir(t, target)
	link := filepath.Join(env.Home, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	first := env.file("secrets", link, "", closed)
	second := env.file("profile", target, "", nil)
	second.Expect, second.ExpectWhy = expectWrite, "rule"
	merged := Merge([]Check{first}, []Check{second, {ID: "network:x", Method: MethodDial}, {ID: "network:x", Method: MethodDial}})
	if len(merged) != 2 || merged[0].ID != "secrets:~/link" || !slices.Equal(merged[0].Expect, expectWrite) {
		t.Errorf("merged = %+v", merged)
	}
}

func sampleReport() Report {
	result := func(group, name string, typ Type, perm Permission, verdict Verdict, detail string, quiet bool) Result {
		return Result{Check: Check{ID: group + ":" + name, Group: group, Type: typ, Name: name, Quiet: quiet, Safe: closed},
			Permission: perm, Verdict: verdict, Detail: detail}
	}
	return Report{
		Environment: Environment{OS: "linux", User: "root"},
		Skipped:     2,
		Results: []Result{
			result("secrets", "~/.aws", TypeFile, Absent, OK, "", false),
			result("secrets", "~/.ssh", TypeFile, Blocked, OK, "", false),
			result("secrets", "~/.npmrc", TypeFile, Read, Risk, "may hold credentials", false),
			result("profile", "~/scratch", TypeFile, Blocked, Unmet, "the profile makes it writable", false),
			result("programs", "git", TypeCapability, Present, OK, "", true),
			result("programs", "ssh", TypeCapability, Present, OK, "", true),
			result("programs", "/usr/bin/git", TypeFile, Read, OK, "", true),
			result("network", "own loopback", TypeNetwork, Connect, OK, "", false),
		},
	}
}

func TestTable(t *testing.T) {
	var out bytes.Buffer
	rep := sampleReport()
	Header(&out, rep.Started, rep.Environment)
	Table(&out, rep, TableOptions{})
	want := `nothing started: checking this environment
linux, user: root, proxy: none, isolation: none detected

profile
  UNMET  blocked  file        ~/scratch     the profile makes it writable

secrets
  RISK   read     file        ~/.npmrc      may hold credentials
  ok     blocked  file        ~/.ssh

programs
  ok     present  capability  git ssh
  ok     read     file        1 more

network
  ok     connect  network     own loopback

1 RISK, 1 UNMET, 6 ok (4 not listed one by one: --all), 2 skipped
`
	if out.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", out.String(), want)
	}

	out.Reset()
	Table(&out, rep, TableOptions{Quiet: true})
	if got := out.String(); strings.Contains(got, "ok   ") || strings.Contains(got, "programs") || !strings.Contains(got, "~/.npmrc") || !strings.Contains(got, "~/scratch") {
		t.Errorf("--quiet keeps only the rows to look at:\n%s", got)
	}

	out.Reset()
	Table(&out, rep, TableOptions{All: true})
	if got := out.String(); !strings.Contains(got, "~/.aws") || !strings.Contains(got, "/usr/bin/git") || strings.Contains(got, "not listed") {
		t.Errorf("--all lists every row:\n%s", got)
	}

	out.Reset()
	Table(&out, rep, TableOptions{Color: true})
	if got := out.String(); !strings.Contains(got, "\033[31mRISK \033[0m") || !strings.Contains(got, "\033[33mUNMET\033[0m") {
		t.Errorf("colours:\n%q", got)
	}

	out.Reset()
	Header(&out, []string{"fence sandbox, profile x"}, rep.Environment)
	if !strings.HasPrefix(out.String(), "started: fence sandbox, profile x\n") {
		t.Errorf("header of a run that started something:\n%s", out.String())
	}
}

// Many paths that do not exist and can be created are one row: they follow
// from one writable folder. An ok row with nothing there is left out.
func TestTableFoldsCreatablePaths(t *testing.T) {
	rep := Report{Environment: Environment{OS: "linux", User: "root"}}
	for _, name := range []string{"~/.ssh", "~/.aws", "~/.kube", "~/.netrc"} {
		rep.Results = append(rep.Results, Result{Check: Check{Group: "secrets", Type: TypeFile, Name: name}, Permission: Create, Verdict: Risk, Detail: "may hold credentials"})
	}
	rep.Results = append(rep.Results,
		Result{Check: Check{Group: "secrets", Type: TypeFile, Name: "~/.npmrc"}, Permission: Read, Verdict: Risk},
		Result{Check: Check{Group: "git", Type: TypeFile, Name: "~/.gitconfig"}, Permission: Create, Verdict: Risk},
		Result{Check: Check{Group: "sockets", Type: TypeFile, Name: "/var/run/docker.sock"}, Permission: Create, Verdict: OK})
	var out bytes.Buffer
	Table(&out, rep, TableOptions{})
	want := `
secrets
  RISK   create  file  4 paths       nothing there yet, each can be created: --all
  RISK   read    file  ~/.npmrc

git
  RISK   create  file  ~/.gitconfig

6 RISK, 0 UNMET, 1 ok (5 not listed one by one: --all), 0 skipped
`
	if out.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", out.String(), want)
	}
	out.Reset()
	Table(&out, rep, TableOptions{All: true})
	if got := out.String(); !strings.Contains(got, "~/.kube") || !strings.Contains(got, "/var/run/docker.sock") || strings.Contains(got, "4 paths") {
		t.Errorf("--all lists each path:\n%s", got)
	}
}

func TestJSON(t *testing.T) {
	var out bytes.Buffer
	if err := JSON(&out, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Started []string
		Skipped int
		Checks  []map[string]any
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if doc.Started == nil || len(doc.Started) != 0 || doc.Skipped != 2 || len(doc.Checks) != 8 {
		t.Errorf("document = %+v", doc)
	}
	first := doc.Checks[0]
	if first["id"] != "secrets:~/.aws" || first["permission"] != "absent" || first["verdict"] != "ok" || first["target"] != "~/.aws" {
		t.Errorf("every check is in the document, the hidden ones too: %v", first)
	}
}

func TestList(t *testing.T) {
	all := []Check{
		{ID: "secrets:~/.ssh", Group: "secrets", Type: TypeFile, Safe: closed},
		{ID: "profile:~/x", Group: "profile", Type: TypeFile, Expect: expectWrite, ExpectWhy: "the profile makes it writable"},
		{ID: "docker-build", Group: "docker", Type: TypeCapability, Active: true, Starts: "builds one image"},
	}
	var out bytes.Buffer
	List(&out, all, all[:2], false)
	for _, want := range []string{"on   file        secrets:~/.ssh", "off  capability  docker-build  (starts: builds one image)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--list misses %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	List(&out, all, all[:2], true)
	got := out.String()
	if strings.Contains(got, "docker-build") || !strings.Contains(got, "expects write|create: the profile makes it writable") || !strings.Contains(got, "safe when blocked|absent") {
		t.Errorf("--dry-run lists what runs and what it is compared with:\n%s", got)
	}
}
