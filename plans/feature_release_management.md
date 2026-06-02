# Release Management — Easier Install for ateam

Today the only install path is `git clone && ./install.sh`, which builds
from source and symlinks the binary into `~/.local/bin/`. This works but
gates adoption on having Go (or installing it via the script) and on the
user being comfortable cloning a repo.

This plan covers options to make ateam installable with a single command,
including Homebrew. The goal is to lower the friction for new users
without rewriting how `findLinuxBinary` discovers the linux companion at
runtime.

## Current state (what the install can already assume)

- `defaults/` is `embed.FS` — the binary is fully self-contained; no
  config files, no `/etc/` templating, no init step beyond `ateam init`.
- Linux companion is **already optional at runtime**.
  `internal/container` calls `findLinuxBinary(orgDir)` in `cmd/table.go`,
  which:
  1. uses the running binary itself if on linux,
  2. otherwise looks in `build/`, next to the host binary, in
     `<org>/cache/`,
  3. falls back to cross-compiling on demand if `go` is in `PATH`,
  4. otherwise warns and returns "" — non-docker profiles keep working.
- A release that drops the linux companion next to the host binary fits
  search step (2) verbatim. No code changes needed to support shipped
  archives.

## The blocker for any official install path: module path mismatch

`go.mod` declares `module github.com/ateam` but the repo is hosted at
`github.com/voidexpr/ateam`. Both `go install` and goreleaser require
these to match. Fix:

```
go mod edit -module github.com/voidexpr/ateam
# then sweep imports: github.com/ateam/... -> github.com/voidexpr/ateam/...
```

Mechanical change but touches every Go file. Worth doing once regardless
of which install path is picked, because every option below depends on it.

## Option 1 — `go install` only (smallest win, ~10 min after the rename)

```bash
go install github.com/voidexpr/ateam@latest
```

**Pros**

- Zero release infra.
- Self-contained binary thanks to `embed.FS`.

**Cons**

- Users must have Go installed.
- Linux companion isn't bundled — relies on `findLinuxBinary` step 5
  cross-compiling on demand (same as today's `make build`).
- No checksums, no signed artifacts, no version pinning beyond go module
  proxy semantics.

**Verdict**: useful for Go developers, not a real install story for
end users.

## Option 2 — GitHub Releases + curl one-liner (the real win, ~half day)

Add **goreleaser** + a tag-triggered GitHub Action. One config builds:

- `darwin/amd64`, `darwin/arm64`
- `linux/amd64`, `linux/arm64`

Trick for the macOS-needs-linux-companion case: goreleaser `extra_files`
ships the **same-arch linux binary inside each darwin archive**, dropped
next to `ateam`. That path is exactly `findLinuxBinary` search step (3),
so no runtime code changes are needed.

User-facing install becomes:

```bash
curl -fsSL https://github.com/voidexpr/ateam/releases/latest/download/install.sh | sh
```

…or, for users with `eget`:

```bash
eget voidexpr/ateam
```

**Work breakdown**

1. Decide version strategy: derive from git tag rather than the static
   `VERSION` file. Update Makefile `LDFLAGS` to fall through to `git
   describe --tags` when `VERSION` is absent.
2. Write `.goreleaser.yml`:
   - `builds:` for darwin/linux × amd64/arm64
   - `archives:` with `extra_files` injecting the matching
     `ateam-linux-<arch>` into the darwin archives
   - `checksum:`, `snapshot:`, `changelog:` defaults
3. Add `.github/workflows/release.yml` triggered on `v*` tag push,
   running goreleaser with `GITHUB_TOKEN`.
4. Ship an `install.sh` in the release assets (or use the upstream
   eget/installer pattern).
5. Update `README.md` Quick Start to lead with the curl one-liner; keep
   the `git clone` path as the "build from source" section.

**Pros**

- No Go required for end users.
- Checksums and signed archives via goreleaser.
- Linux companion bundled for the docker use case.
- Becomes the foundation for Option 3 with almost no extra work.

**Cons**

- Every fix users want now needs a tag + release, not just `git pull &&
  make build`. Real cost for a tool moving fast pre-1.0.

## Option 3 — Homebrew tap (adds ~1 hour on top of Option 2)

goreleaser has a `brews:` section that auto-pushes a formula to a tap
repo (e.g. `voidexpr/homebrew-tap`) on each release. Users:

```bash
brew tap voidexpr/ateam
brew install ateam
```

The formula can install the linux companion as an extra resource for the
docker workflow, or be split into `ateam` (slim, no companion) and
`ateam-with-docker` (bundled) if that distinction matters.

**Homebrew-core** (so `brew install ateam` works without a tap) has
notability + maturity requirements — minimum stargazer counts, multi-PM
presence, age. Probably not yet realistic. A user tap is the right
starting point and can graduate to core later.

**Pros**

- Native macOS install + `brew upgrade` path.
- Effort is mostly already done by goreleaser config.

**Cons**

- Tap repo to maintain (one-time setup, near-zero ongoing work because
  goreleaser pushes the formula).

## Recommendation

Do **Option 2 + Option 3 together** as one project. They share the
goreleaser config; the homebrew tap is a few extra lines once releases
exist. Skip Option 1 unless it's specifically wanted for the Go-dev
flow, because once releases exist, `go install` is the *worse* path
(slower, needs Go, no checksums, no bundled companion).

Sequence:

1. **Rename the module** to `github.com/voidexpr/ateam` and sweep
   imports. This unblocks everything else and is a one-time cost.
2. **Switch version derivation** to git tag (drop static `VERSION` file,
   or treat it as a fallback for non-tag builds).
3. **Add goreleaser** + release workflow. Confirm the darwin archives
   contain the matching linux companion. Manually test
   `curl|sh` against a pre-release tag.
4. **Add the brews block** + create `voidexpr/homebrew-tap` repo. Test
   `brew install` end to end.
5. **Update README** to lead with the easy paths; demote `git clone`.

## The "make companion optional" caveat

Already true at runtime — see the `findLinuxBinary` behavior above. No
code changes required. The decision is just whether to **bundle** the
companion in the release archives or not. Recommendation: bundle. The
binary is small, docker is a common path, and unbundling saves nothing
meaningful while making the docker UX worse.

If you ever want to give users an explicit choice, the cleanest split is
two homebrew formulas (`ateam` slim, `ateam-with-docker` bundled) — but
that's optional polish, not a v1 concern.

## Main tradeoff to accept

Doing this well means **the release pipeline becomes the source of
truth for "installed" ateam**. That's good for users (predictable
versions, checksums, `brew upgrade`) but slightly less flexible for
development — every user-facing fix wants a tag + release rather than
"pull main and rebuild". For pre-1.0, that's a real cost worth naming.

If that cost feels too high right now, fall back to **Option 1 alone**:
rename the module so `go install github.com/voidexpr/ateam@latest`
works, leave releases for later. It's a 10-minute fix that unlocks the
Go-dev path with no infra commitment, and Option 2 stays available for
when the tool stabilizes.

## Out of scope (for now)

- `apt` / `dnf` / `rpm` packaging — every Linux distro is its own
  release pipeline; the static binary in a tarball is fine for now.
- Windows builds — no current users, no demand surfaced.
- Auto-update from inside the CLI (`ateam self-update`) — possible later
  via the GitHub releases API, but not needed if `brew upgrade` /
  `curl|sh` covers most users.
- Code signing / notarization on macOS — defer until users actually hit
  Gatekeeper warnings in practice. goreleaser can wire this in later.
- Homebrew-core submission — defer until notability bar is clearly met.
