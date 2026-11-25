package sandboxprobe

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Programs are the programs the built-in checks look for.
var Programs = []string{
	"sh", "bash", "zsh", "git", "ssh", "sudo", "curl", "python3", "node", "go", "make",
	"claude", "codex", "nono", "fence", "docker", "podman", "limactl", "colima",
	"playwright-cli", "npx", "bwrap", "socat", "tmux", "gh", "aws", "gcloud", "kubectl",
	"npm", "uv", "bun", "pip3",
}

const (
	whyStartup = "runs in every later shell"
	whySecret  = "may hold credentials"
	whyAgent   = "steers the agent's next run"
	whyAteam   = "ateam's own files: they set the rules of the next launch"
	whyGit     = "git runs or reads it outside the sandbox later"
	whyReplace = "a command can replace it"
	whySocket  = "a command can use what listens there"
)

// Defaults returns the built-in checks for env: the fixed lists. programs
// are extra program names.
func Defaults(env Env, programs []string) []Check {
	return slices.Concat(
		whereChecks(env), secretChecks(env), shellChecks(env), agentChecks(env), ateamChecks(env),
		gitChecks(env), programChecks(env, programs), socketChecks(env), capabilityChecks(env), networkChecks(),
	)
}

func whereChecks(env Env) []Check {
	checks := []Check{
		env.file("where", env.WorkDir, "", nil),
		env.file("where", env.Home, "everything in the home can be changed", safe(Read, Blocked)),
	}
	for _, dir := range []string{env.TmpDir, "/tmp"} {
		if dir != "" {
			checks = append(checks, env.file("where", filepath.Clean(dir), "", nil))
		}
	}
	return checks
}

func secretChecks(env Env) []Check {
	return env.files("secrets", whySecret, closed, env.Secrets...)
}

func shellChecks(env Env) []Check {
	var files []string
	switch filepath.Base(env.Shell) {
	case "bash":
		files = []string{".bashrc", ".bash_profile", ".profile"}
	case "zsh":
		files = []string{".zshrc", ".zprofile", ".zshenv"}
	case "fish":
		files = []string{".config/fish/config.fish"}
	}
	var checks []Check
	for _, file := range files {
		checks = append(checks, env.file("shell", env.home(file), whyStartup, readOnly))
	}
	return checks
}

func agentChecks(env Env) []Check {
	var paths []string
	if dir := env.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		paths = append(paths, dir, filepath.Join(dir, "settings.json"))
	} else {
		paths = append(paths, env.home(".claude"), env.home(".claude", "settings.json"), env.home(".claude.json"))
	}
	codex := env.Getenv("CODEX_HOME")
	if codex == "" {
		codex = env.home(".codex")
	}
	paths = append(paths, codex, filepath.Join(codex, "config.toml"), filepath.Join(codex, "auth.json"),
		env.work(".claude"), env.work(".mcp.json"))
	return env.files("agent", whyAgent, readOnly, paths...)
}

func ateamChecks(env Env) []Check {
	project := env.ProjectDir
	if project == "" {
		project = env.work(".ateam")
	}
	var paths []string
	if env.AteamHome != "" {
		for _, name := range []string{"ateam.hcl", "secrets.env", "profiles"} {
			paths = append(paths, filepath.Join(env.AteamHome, name))
		}
	}
	for _, name := range []string{"ateam.hcl", "secrets.env", "profiles", "state.sqlite"} {
		paths = append(paths, filepath.Join(project, name))
	}
	return env.files("ateam", whyAteam, closed, paths...)
}

// gitChecks covers what git runs or reads on its own later: the
// repository's config, the user's, and the hooks, each one by name.
func gitChecks(env Env) []Check {
	paths := []string{env.home(".gitconfig"), env.home(".config", "git", "config")}
	hooks := gitConfigValue(paths[0], "core", "hookspath")
	if common := gitCommonDir(env.WorkDir); common != "" {
		config := filepath.Join(common, "config")
		paths = append(paths, config)
		if own := gitConfigValue(config, "core", "hookspath"); own != "" {
			hooks = own
		}
		if hooks == "" {
			hooks = filepath.Join(common, "hooks")
		}
	}
	if hooks != "" {
		switch {
		case strings.HasPrefix(hooks, "~/"):
			hooks = env.home(hooks[2:])
		case !filepath.IsAbs(hooks):
			hooks = env.work(hooks)
		}
		paths = append(paths, hooks)
		entries, _ := os.ReadDir(hooks)
		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasSuffix(entry.Name(), ".sample") {
				paths = append(paths, filepath.Join(hooks, entry.Name()))
			}
		}
	}
	return env.files("git", whyGit, readOnly, paths...)
}

// gitCommonDir returns the folder holding a working folder's config and
// hooks: .git, or for a worktree the repository's own .git. "" without a
// repository.
func gitCommonDir(work string) string {
	dotGit := filepath.Join(work, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotGit
	}
	gitDir := pointerFile(dotGit, "gitdir: ", work)
	if gitDir == "" {
		return ""
	}
	if common := pointerFile(filepath.Join(gitDir, "commondir"), "", gitDir); common != "" {
		return common
	}
	return gitDir
}

// pointerFile reads a one-line file that names a folder, relative to base
// when not absolute.
func pointerFile(file, prefix, base string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	line, ok := strings.CutPrefix(strings.TrimSpace(string(data)), prefix)
	if !ok || line == "" {
		return ""
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(base, line)
	}
	return filepath.Clean(line)
}

// gitConfigValue returns one key of a git config file, "" when the file or
// the key is missing. Only the section and key asked for are kept.
func gitConfigValue(file, section, key string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	value, current := "", ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			current = strings.ToLower(strings.Trim(line, "[] \t"))
			continue
		}
		name, val, ok := strings.Cut(line, "=")
		if ok && current == section && strings.ToLower(strings.TrimSpace(name)) == key {
			value = strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	return value
}

// programChecks returns, per program, whether it is there and whether its
// file can be replaced, then every folder of PATH: a writable one shadows
// the programs found after it.
func programChecks(env Env, extra []string) []Check {
	names := slices.Concat([]string{"ateam"}, Programs, extra)
	if shell := filepath.Base(env.Shell); env.Shell != "" && !slices.Contains(names, shell) {
		names = append(names, shell)
	}
	var checks []Check
	resolved := map[string]string{}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		target := name
		if name == "ateam" && env.Executable != "" {
			target = env.Executable
		}
		checks = append(checks, Check{ID: "programs:" + name, Group: "programs", Type: TypeCapability,
			Method: MethodProgram, Target: target, Name: name, Quiet: true})
		if path := env.lookPath(target); path != "" {
			resolved[name] = path
			checks = append(checks, Check{ID: "programs:" + name + ":file", Group: "programs", Type: TypeFile,
				Method: MethodProgramFile, Target: path, Name: env.name(path), Why: whyReplace + ": " + name,
				Safe: readOnly, Quiet: true})
		}
	}
	dirs := map[string]bool{}
	for i, dir := range env.Path {
		if dir == "" || dirs[dir] {
			continue
		}
		dirs[dir] = true
		var after []string
		for _, name := range names {
			path := resolved[name]
			if path != "" && slices.Contains(env.Path[i+1:], filepath.Dir(path)) && !slices.Contains(after, name) {
				after = append(after, name)
			}
		}
		why := "a command can add a program to this folder of PATH"
		if len(after) > 0 {
			why = "a program put here runs instead of: " + strings.Join(after[:min(len(after), 5)], ", ")
			if len(after) > 5 {
				why += ", ..."
			}
		}
		check := env.file("programs", filepath.Clean(dir), why, readOnly)
		check.ID = "programs:PATH:" + check.Name
		check.Quiet = true
		checks = append(checks, check)
	}
	return checks
}

func socketChecks(env Env) []Check {
	paths := []string{env.Getenv("SSH_AUTH_SOCK")}
	paths = append(paths, dockerSockets(env)...)
	if run := env.Getenv("XDG_RUNTIME_DIR"); run != "" {
		paths = append(paths, filepath.Join(run, "podman", "podman.sock"), filepath.Join(run, "gnupg", "S.gpg-agent"))
	}
	paths = append(paths, "/run/containerd/containerd.sock", env.home(".gnupg", "S.gpg-agent"))
	var checks []Check
	for _, path := range paths {
		if path != "" {
			// A socket path that is free is no way into anything.
			checks = append(checks, env.file("sockets", path, whySocket, safe(Blocked, Absent, Create)))
		}
	}
	return checks
}

// dockerSockets lists where a Docker daemon's socket may be, the one the
// environment names first.
func dockerSockets(env Env) []string {
	var paths []string
	if path, ok := strings.CutPrefix(env.Getenv("DOCKER_HOST"), "unix://"); ok {
		paths = append(paths, path)
	}
	paths = append(paths, "/var/run/docker.sock")
	if run := env.Getenv("XDG_RUNTIME_DIR"); run != "" {
		paths = append(paths, filepath.Join(run, "docker.sock"))
	}
	return append(paths, env.home(".docker", "run", "docker.sock"), env.home(".colima", "default", "docker.sock"))
}

func capabilityChecks(env Env) []Check {
	browsers := env.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	switch {
	case browsers != "":
	case env.GOOS == "darwin":
		browsers = env.home("Library", "Caches", "ms-playwright")
	default:
		browsers = env.home(".cache", "ms-playwright")
	}
	capability := func(group, name string, method Method, target string, safe []Permission, why string) Check {
		return Check{ID: group + ":" + name, Group: group, Type: TypeCapability, Method: method, Target: target, Name: name, Safe: safe, Why: why}
	}
	checks := []Check{
		capability("docker", "docker daemon", MethodDocker, strings.Join(dockerSockets(env), "\n"), safe(Absent),
			"a command that reaches the daemon can run anything the daemon can"),
		env.file("playwright", browsers, "", nil),
		capability("identity", "root", MethodRoot, "", safe(Absent), "the command runs as root"),
		capability("identity", "group docker", MethodGroup, "docker", safe(Absent), "the docker group reaches the daemon"),
	}
	playwright := capability("playwright", "playwright-cli", MethodProgram, "playwright-cli", nil, "")
	playwright.Note = "not launched: --with playwright-launch"
	checks = append(checks, playwright)
	for _, group := range []string{"sudo", "wheel", "admin"} {
		checks = append(checks, capability("identity", "group "+group, MethodGroup, group, nil, ""))
	}
	if env.GOOS == "linux" {
		checks = append(checks, capability("identity", "linux capabilities", MethodCaps, "/proc/self/status", safe(Absent),
			"more than an unprivileged process has"))
	}
	return checks
}

func networkChecks() []Check {
	network := func(name string, method Method, target string) Check {
		return Check{ID: "network:" + name, Group: "network", Type: TypeNetwork, Method: method, Target: target, Name: name}
	}
	metadata := network("169.254.169.254:80", MethodDial, "169.254.169.254:80")
	metadata.Safe, metadata.Why = safe(Blocked), "cloud metadata: it hands out the machine's credentials"
	return []Check{
		network("127.0.0.1:0 (listen)", MethodListen, "127.0.0.1:0"),
		network("0.0.0.0:0 (listen)", MethodListen, "0.0.0.0:0"),
		network("own loopback", MethodLoopback, ""),
		network("DNS example.com", MethodDNS, "example.com"),
		URLCheck("network", "https://example.com", MethodURLProxy),
		URLCheck("network", "https://example.com", MethodURLDirect),
		network("1.1.1.1:443 (direct)", MethodDial, "1.1.1.1:443"),
		metadata,
	}
}

// URLCheck returns the check that reaches url, through the proxy
// (MethodURLProxy) or directly (MethodURLDirect).
func URLCheck(group, url string, method Method) Check {
	name := url + " (direct)"
	if method == MethodURLProxy {
		name = url + " (proxy)"
	}
	return Check{ID: group + ":" + name, Group: group, Type: TypeNetwork, Method: method, Target: url, Name: name}
}
