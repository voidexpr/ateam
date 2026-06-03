# Auto-detect ateam-inside-a-sandbox (fence, Seatbelt, bubblewrap)

## Problem

When ateam runs inside an outer sandbox (e.g. `fence` on the host), the
coding agents it spawns try to apply their *own* inner sandbox
(claude → Seatbelt via `sandbox-exec` on macOS / bwrap on Linux; codex
likewise). Nested sandboxes fail:

```bash
$ fence sandbox-exec -p '(version 1)(allow default)' /usr/bin/true
sandbox-exec: sandbox_apply: Operation not permitted
```

For Docker, ateam already solves this by detecting "I'm in a container"
and switching the agent config so the inner sandbox is skipped and the
agent runs with `--dangerously-skip-permissions`. We want the same
behavior whenever ateam is inside *any* outer isolation layer, not just
Docker / Podman.

## Current state (what already works)

- `internal/container/container.go` exposes `IsInContainer()` which
  returns true if any of:
  - `ATEAM_IN_CONTAINER=1` env var (manual override)
  - `/.dockerenv` exists
  - `/run/.containerenv` exists (Podman)
- Agent configs in `defaults/runtime.hcl` already have the dual knobs:
  ```hcl
  args_inside_container    = ["--dangerously-skip-permissions"]
  sandbox_inside_container = false
  ```
- The wiring is plumbed all the way through `cmd/table.go` and
  `internal/runtime/config.go` — every place that needs to know
  "skip inner sandbox" already asks `IsInContainer()`.

What's missing is a fourth detection signal. The agent-config side of
the problem is already done.

## Detection options, by OS

### Cooperative signals (env vars / marker files)

The cheapest and most reliable signals: the outer sandbox announces
itself. We trust these without any probing.

| Source | Set by | Detected via |
|---|---|---|
| `ATEAM_IN_CONTAINER=1` | user (existing) | env |
| `ATEAM_IN_SANDBOX=1` | user (new generic override) | env |
| `FENCE_SANDBOX=1` | fence (confirmed: `fence env` shows it) | env |
| `FIREJAIL_NAME` | firejail | env |
| `container=<runtime>` | systemd-detect-virt convention | env |
| `/.dockerenv` | Docker (existing) | file |
| `/run/.containerenv` | Podman (existing) | file |

### macOS — Seatbelt probe

When fence (or any outer Seatbelt user) has already applied a profile,
attempting to apply a second one fails immediately:

```bash
sandbox-exec -p '(version 1)(allow default)' /usr/bin/true
```

Exit 0 outside any sandbox; exit non-zero with
`sandbox_apply: Operation not permitted` inside. Measured at ~46ms on
the test machine — cheap enough to run once per ateam startup and
cache with `sync.Once`.

No clean alternative — Apple deliberately doesn't expose
"am I sandboxed" to userspace. Ancestor walk via `ps -p $PPID -o
comm=` up to PID 1 looking for `sandbox-exec` would also work, but it
requires knowing all possible ancestor names (`fence`, `sandbox-exec`,
…). The probe sidesteps that.

### Linux — /proc heuristics

No single bwrap-specific signal exists (bwrap doesn't drop a marker
file like Docker does), but three cheap `/proc` reads cover the cases
we care about:

1. **User-namespace divergence** — strongest single signal.
   `readlink /proc/self/ns/user` ≠ `readlink /proc/1/ns/user` means we
   were placed in a new user namespace. True under bwrap, firejail,
   Docker, Podman, and most modern sandbox tools by default.

2. **`/proc/self/status` Seccomp / NoNewPrivs** — strong.
   - `Seccomp: 2` → seccomp filter active.
   - `NoNewPrivs: 1` → cannot acquire privileges.
   Bwrap defaults to setting both.

3. **`container=` env var** — systemd-detect-virt and some sandbox
   tools export this with the runtime name.

Distinguishing *which* outer sandbox is diagnostic, not functional:
walk `/proc/<pid>/status:PPid:` up to PID 1 and check
`/proc/<pid>/comm` for `bwrap`, `firejail`, `fence`, etc. Useful for
logging, not for behavior.

**False positive worth naming**: systemd-hardened user services may
set `Seccomp:` + `NoNewPrivs:` even when no outer sandbox is around.
Treating those as "isolated" is actually safer here (the agent's inner
sandbox may genuinely fail to nest), so the bias is acceptable.

## Proposed design

### Split signals by trust level

| Layer | Always on | Toggleable |
|---|---|---|
| Explicit ateam overrides (`ATEAM_IN_CONTAINER`, `ATEAM_IN_SANDBOX`) | ✓ | |
| Container markers (`/.dockerenv`, `/run/.containerenv`) | ✓ | |
| Cooperative third-party env vars (`FENCE_SANDBOX`, `FIREJAIL_NAME`, `container=`) | | ✓ |
| macOS Seatbelt probe | | ✓ |
| Linux `/proc` heuristics | | ✓ |

**Always-on** signals preserve today's behavior exactly. A user who
disables auto-detection can still force the right answer by exporting
`ATEAM_IN_SANDBOX=1`.

**Toggleable** signals are guesses about *other* tools — fine by default,
but the user can turn them off if they cause a false positive.

### Configuration surface

Two layers, no `config.toml`:

| Layer | Form | Use case |
|---|---|---|
| CLI flag | `--sandbox-detection=true\|false` | one-off override |
| `runtime.hcl` top-level | `sandbox_detection = true\|false` | persistent default (drop into `~/.ateamorg/runtime.hcl`) |
| embedded default | `true` | ship safe |

Precedence: CLI > runtime.hcl > default.

Skipping `config.toml`: whether you're in an outer sandbox is a
property of your shell session, not the project. Per-project override
has no clear use case. If one emerges later, adding a third layer is
cheap.

### Widen `IsInContainer` rather than introduce a parallel concept

Renaming would mean churning every call site and every config key.
Cheaper: widen the semantics of `IsInContainer` to mean "ateam is
already inside some isolation layer", and add an `IsolationSource()`
helper for diagnostics. Document the wider meaning in a comment +
ISOLATION.md. `args_inside_container` / `sandbox_inside_container`
keep their names; their behavior just triggers more often.

Eventually (Phase 2 below), if fence becomes a first-class isolation
*mechanism* that ateam invokes (not just an outer wrapper it tolerates),
we add a `FenceContainer` next to `DockerContainer`. That's a separate
concern from detection.

### Surface the decision in `ateam env`

Print the source so users can debug:

```
Isolation: detected (source: fence via $FENCE_SANDBOX)
Isolation: detected (source: macos:seatbelt via probe)
Isolation: detected (source: linux:userns-diverged)
Isolation: none
Isolation: skipped (--sandbox-detection=false)
```

`--verbose` on any command should log the same line.

## Implementation sketch

### `internal/container/container.go`

```go
// IsInContainer reports whether ateam is already inside an isolation
// layer (Docker/Podman, or another sandbox like fence). Agents called
// from inside such an environment should skip their own inner sandbox
// because nested sandboxing is generally not supported.
func IsInContainer() bool {
    return IsolationSource() != ""
}

// IsolationSource returns a short identifier describing why ateam
// thinks it is isolated, or "" if it isn't. Cached for the process
// lifetime.
func IsolationSource() string {
    isoOnce.Do(func() { isoSource = detectIsolation() })
    return isoSource
}

var (
    isoOnce   sync.Once
    isoSource string
)

func detectIsolation() string {
    // ── Always-on signals ────────────────────────────────────────
    if os.Getenv("ATEAM_IN_CONTAINER") == "1" {
        return "env:ATEAM_IN_CONTAINER"
    }
    if os.Getenv("ATEAM_IN_SANDBOX") == "1" {
        return "env:ATEAM_IN_SANDBOX"
    }
    for _, m := range []string{"/.dockerenv", "/run/.containerenv"} {
        if _, err := os.Stat(m); err == nil {
            return "marker:" + m
        }
    }

    // ── Toggleable detection ─────────────────────────────────────
    if !SandboxDetectionEnabled() {
        return ""
    }

    if os.Getenv("FENCE_SANDBOX") != "" {
        return "fence"
    }
    if os.Getenv("FIREJAIL_NAME") != "" {
        return "firejail"
    }
    if v := os.Getenv("container"); v != "" {
        return "container=" + v
    }
    if s := detectOSSandbox(); s != "" {
        return s
    }
    return ""
}
```

### `internal/container/isolation_darwin.go`

```go
func detectOSSandbox() string {
    cmd := exec.Command("sandbox-exec",
        "-p", "(version 1)(allow default)", "/usr/bin/true")
    if err := cmd.Run(); err != nil {
        return "macos:seatbelt"
    }
    return ""
}
```

### `internal/container/isolation_linux.go`

```go
func detectOSSandbox() string {
    if selfNS, err := os.Readlink("/proc/self/ns/user"); err == nil {
        if pid1NS, err := os.Readlink("/proc/1/ns/user"); err == nil {
            if selfNS != "" && pid1NS != "" && selfNS != pid1NS {
                return "linux:userns-diverged"
            }
        }
    }
    if data, err := os.ReadFile("/proc/self/status"); err == nil {
        for _, line := range strings.Split(string(data), "\n") {
            switch {
            case strings.HasPrefix(line, "Seccomp:"):
                if v := strings.TrimSpace(strings.TrimPrefix(line, "Seccomp:")); v != "0" {
                    return "linux:seccomp"
                }
            case strings.HasPrefix(line, "NoNewPrivs:"):
                if strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:")) == "1" {
                    return "linux:nnp"
                }
            }
        }
    }
    return ""
}
```

### `internal/container/isolation_other.go` (stub for non-darwin / non-linux)

```go
//go:build !darwin && !linux

func detectOSSandbox() string { return "" }
```

### Toggle plumbing

```go
// SandboxDetectionEnabled returns true when ateam should run the
// toggleable detection probes. Driven by --sandbox-detection,
// runtime.hcl, or the default (true).
func SandboxDetectionEnabled() bool {
    if v := sandboxDetectionOverride.Load(); v != nil {
        return *v
    }
    return true // default
}

var sandboxDetectionOverride atomic.Pointer[bool]

func SetSandboxDetection(enabled bool) {
    sandboxDetectionOverride.Store(&enabled)
}
```

- CLI flag (in the persistent-flag setup): `--sandbox-detection` parsed
  to a bool; on root cobra `PersistentPreRun`, call
  `container.SetSandboxDetection(...)` if the flag was set.
- `runtime.hcl` top-level scalar: extend the HCL schema in
  `internal/runtime/config.go` with `SandboxDetection *bool`. Resolve
  in the order CLI > runtime.hcl > default and call `SetSandboxDetection`
  at startup once the runtime config is loaded.

### `ateam env` output

Add one line in `cmd/env.go`:

```go
src := container.IsolationSource()
switch {
case !container.SandboxDetectionEnabled():
    fmt.Println("Isolation: skipped (--sandbox-detection=false)")
case src == "":
    fmt.Println("Isolation: none")
default:
    fmt.Println("Isolation: detected (source: " + src + ")")
}
```

## Phasing

### Phase 1 — Detect & honor (this plan)

Everything above. Ships fence, firejail, generic macOS Seatbelt, and
generic Linux bwrap-style detection as a transparent extension of the
existing container detection. No new agent code, no new config blocks,
just one wider helper and one toggle.

### Phase 2 — Fence as a first-class isolation mechanism (later, optional)

If/when we want `ateam --profile fence` (ateam invokes fence to
isolate the agents, rather than tolerating an outer fence), add a
`FenceContainer` implementing `internal/container.Container` next to
`DockerContainer` / `DockerExecContainer`. Separate concern; not part
of this plan. Worth doing only when there's a clear use case beyond
"fence works if you wrap ateam in it".

## Documentation work

- Update `ISOLATION.md`:
  - Add a "When ateam is already inside an outer sandbox" section.
  - Note that the behavior is automatic for fence / firejail / Seatbelt /
    Linux bwrap-style isolation, and how to override with
    `--sandbox-detection=false` or `ATEAM_IN_SANDBOX=1`.
  - Document the false-positive cases (systemd-hardened user services).
- Update `COMMANDS.md` global-flags table with `--sandbox-detection`.
- Update `CONFIG.md` with the new runtime.hcl scalar.
- Update `README.md` brief mention only — the existing one-liner about
  "Docker / sandbox" already covers most of it.

## Testing

- Unit: each `detectOSSandbox` returns expected strings against
  synthesized `/proc` data and stubbed `sandbox-exec` for darwin (the
  darwin path is genuinely an integration test — gate it on
  `runtime.GOOS == "darwin"`).
- Unit: signal precedence — explicit env var beats marker beats
  cooperative env var beats OS probe.
- Unit: `SandboxDetectionEnabled() == false` suppresses cooperative
  env vars and OS probe but not always-on signals.
- CLI integration (`test/cli/`): set each env var in turn, run
  `ateam env`, assert the expected `Isolation:` line.
- Manual smoke test under fence on macOS (the user's environment): run
  `fence ateam env` and confirm `Isolation: detected (source: fence
  via $FENCE_SANDBOX)`. Then run a tiny `ateam exec` against a mock
  agent inside fence and confirm `--dangerously-skip-permissions` is
  used and no `sandbox-exec` invocation appears in the resolved
  command (visible via `ateam exec --dry-run`).
- Manual smoke test under bwrap on Linux: same idea, expecting
  `linux:userns-diverged` or `linux:seccomp` as the source.

## Out of scope

- Per-project override via `config.toml`. Add only if a real use case
  appears.
- Phase 2 — fence as a `Container` implementation.
- Detecting Landlock — no clean userspace check, and ateam doesn't
  care which exact mechanism the outer sandbox uses, only that
  *something* is there.
- macOS code-signing / notarization concerns under fence — outside the
  detection scope.
- Cross-arch / cross-OS sandboxing matrix improvements — separate
  release-management plan.

## Main tradeoff

Every entry in the toggleable row is conservative — when in doubt, it
concludes "isolated", and ateam therefore tells the agent to skip its
own inner sandbox. The cost of a false positive is a silent loss of
defense in depth: on a host where the user *wanted* the agent's inner
sandbox, they don't get it. The escape hatch is
`--sandbox-detection=false`; the fix when a real false positive shows
up is to tighten the specific rule that triggered. Worth a one-line
note in ISOLATION.md so the failure mode is visible to users before
they trip over it.

If that bias feels wrong long-term, the alternative is **explicit
signaling only** — drop the OS probes entirely, require fence/firejail
users to set `ATEAM_IN_SANDBOX=1` themselves (or get the upstream tool
to set it for them). Less magic, more user steps. Probably the right
trade once ateam has more users; for now the auto-detection is what
makes the "just works inside fence" promise actually true.
