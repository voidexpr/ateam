#!/usr/bin/env bash
# Migrate a pre-ateam.hcl layout (~/.ateamorg + per-project config.toml /
# runtime.hcl) to the current one (~/.ateam + ateam.hcl). See CONFIG.md,
# "Migrating from .ateamorg".
#
# What it does, in order:
#   1. ~/.ateam/ateam.hcl        <- ~/.ateamorg/runtime.hcl (config_dir paths rewritten)
#      ~/.ateam/prompts/         <- ~/.ateamorg/prompts/
#      ~/.ateam/Dockerfile       <- ~/.ateamorg/Dockerfile
#      ~/.ateam/secrets.env      <- ~/.config/ateam/secrets.env, then ~/.ateamorg/secrets.env
#      ~/.ateam/state/agents/X   <- every ~/.ateamorg/X that looks like an agent config dir
#      ~/.ateam/state/{state.sqlite,logs,cache} <- scratch-mode state
#   2. For every project registered under ~/.ateamorg/projects/ (and any
#      directories passed as arguments): .ateam/config.toml + runtime.hcl
#      -> .ateam/ateam.hcl, db rows renamed to the new project name, old
#      files kept under .ateam/cache/migrated/.
#   3. ~/.ateamorg -> ~/.ateamorg.migrated (nothing is deleted).
#   4. `ateam project-rename` in each project to register it.
#
# Usage: scripts/migrate-ateamorg.sh [PROJECT_DIR...]
# Needs python3 (3.11+ for TOML) and the new ateam binary on PATH.
set -euo pipefail

old="${ATEAM_LEGACY_ORG:-$HOME/.ateamorg}"
new="${ATEAM_HOME:-$HOME/.ateam}"
ateam="${ATEAM:-ateam}"

say() { printf '%s\n' "$*"; }

if [ ! -d "$old" ]; then
    say "no legacy org at $old; nothing to migrate for the user layer"
fi
if [ -e "$new/ateam.hcl" ]; then
    say "error: $new/ateam.hcl already exists; refusing to overwrite" >&2
    exit 1
fi

mkdir -p "$new/state/agents" "$new/prompts"

# --- user layer -------------------------------------------------------------
agent_dirs=()
if [ -d "$old" ]; then
    for d in "$old"/*/; do
        d="${d%/}"; name="$(basename "$d")"
        case "$name" in defaults|projects|logs|prompts|cache|runtime) continue ;; esac
        if [ -e "$d/.claude.json" ] || [ -e "$d/.credentials.json" ] || [ -e "$d/settings.json" ] || [ -e "$d/auth.json" ] || [ -e "$d/secrets.env" ]; then
            agent_dirs+=("$name")
        fi
    done
    for name in "${agent_dirs[@]:-}"; do
        [ -z "$name" ] && continue
        say "agent config dir: $old/$name -> $new/state/agents/$name"
        mv "$old/$name" "$new/state/agents/$name"
    done
    if [ -f "$old/runtime.hcl" ]; then
        say "config: $old/runtime.hcl -> $new/ateam.hcl"
        {
            echo "// Migrated from ~/.ateamorg/runtime.hcl by scripts/migrate-ateamorg.sh."
            echo "// A block here overrides only the attributes it sets; a --model inside args"
            echo "// should become a model attribute ('ateam env' warns about each one)."
            echo
            cat "$old/runtime.hcl"
        } > "$new/ateam.hcl"
        for name in "${agent_dirs[@]:-}"; do
            [ -z "$name" ] && continue
            sed -i.bak -e "s#~/\\.ateamorg/$name#~/.ateam/state/agents/$name#g" -e "s#$old/$name#$new/state/agents/$name#g" "$new/ateam.hcl"
        done
        rm -f "$new/ateam.hcl.bak"
    fi
    [ -d "$old/prompts" ] && { say "prompts: $old/prompts -> $new/prompts"; cp -R "$old/prompts/." "$new/prompts/"; }
    [ -f "$old/Dockerfile" ] && { say "Dockerfile -> $new/Dockerfile"; cp "$old/Dockerfile" "$new/Dockerfile"; }
    [ -f "$old/state.sqlite" ] && mv "$old/state.sqlite" "$new/state/state.sqlite"
    [ -d "$old/logs" ] && mv "$old/logs" "$new/state/logs"
    [ -d "$old/cache" ] && mv "$old/cache" "$new/state/cache"
fi
for src in "$HOME/.config/ateam/secrets.env" "$old/secrets.env"; do
    if [ -f "$src" ]; then
        if [ -f "$new/secrets.env" ]; then
            say "secrets: appending $src to $new/secrets.env (existing keys win)"
            cat "$src" >> "$new/secrets.env"
        else
            say "secrets: $src -> $new/secrets.env"
            mv "$src" "$new/secrets.env"
        fi
        chmod 600 "$new/secrets.env"
    fi
done
[ -e "$new/ateam.hcl" ] || printf '// User-wide ateam configuration. See CONFIG.md.\n' > "$new/ateam.hcl"

# --- projects ---------------------------------------------------------------
projects=("$@")
if [ -d "$old/projects" ]; then
    orgroot="$(dirname "$old")"
    for d in "$old/projects"/*/; do
        [ -d "$d" ] || continue
        id="$(basename "$d")"
        rel="$(python3 -c 'import sys; s=sys.argv[1]; print(s.replace("__","\0").replace("_","/").replace("\0","_"))' "$id")"
        projects+=("$orgroot/$rel")
    done
fi

convert_project() {
    local root="$1" dir="$1/.ateam"
    [ -d "$dir" ] || { say "skip $root: no .ateam/"; return; }
    if [ ! -f "$dir/config.toml" ]; then
        say "project $root: already on ateam.hcl"
    else
        say "project $root: converting config.toml + runtime.hcl -> ateam.hcl"
        mkdir -p "$dir/cache/migrated"
        python3 - "$root" <<'PY'
import sys, os, re, tomllib, json
root = sys.argv[1]; d = os.path.join(root, ".ateam")
cfg = tomllib.load(open(os.path.join(d, "config.toml"), "rb"))
name = os.path.basename(root).lower()
name = re.sub(r"[^a-z0-9._-]", "-", name).strip("-.") or "project"
out = ["// ateam project configuration (migrated from config.toml by scripts/migrate-ateamorg.sh).",
       "// A block here overrides only the attributes it sets; see CONFIG.md.", "",
       "project {", f"  name = {json.dumps(name)}"]
p = cfg.get("project", {})
if p.get("default_profile"): out.append(f"  default_profile = {json.dumps(p['default_profile'])}")
tm = {"report_max_parallel": cfg.get("report", {}).get("max_parallel"),
      "report_timeout_minutes": cfg.get("report", {}).get("report_timeout_minutes"),
      "review_timeout_minutes": cfg.get("review", {}).get("timeout_minutes"),
      "code_timeout_minutes": cfg.get("code", {}).get("timeout_minutes"),
      "verify_timeout_minutes": cfg.get("verify", {}).get("timeout_minutes"),
      "exec_timeout_minutes": cfg.get("exec", {}).get("timeout_minutes") or cfg.get("run", {}).get("timeout_minutes"),
      "serve_port": cfg.get("serve", {}).get("port")}
defaults = {"report_max_parallel": 3, "report_timeout_minutes": 20, "review_timeout_minutes": 20,
            "code_timeout_minutes": 120, "verify_timeout_minutes": 30, "exec_timeout_minutes": 60, "serve_port": 0}
for k, v in tm.items():
    if v not in (None, defaults[k]): out.append(f"  {k} = {v}")
out += ["}", ""]
roles = cfg.get("roles", {})
profiles = cfg.get("profiles", {}).get("roles", {})
for r in sorted(set(roles) | set(profiles)):
    attrs = []
    if r in roles: attrs.append(f'status = {json.dumps("on" if roles[r] in ("on", "enabled") else "off")}')
    if profiles.get(r): attrs.append(f"profile = {json.dumps(profiles[r])}")
    if len(attrs) == 1:
        out.append("role %-28s { %s }" % (json.dumps(r), attrs[0]))
    else:  # a single-line HCL block holds one attribute only
        out += [f"role {json.dumps(r)} {{"] + [f"  {a}" for a in attrs] + ["}"]
sup = {k: v for k, v in cfg.get("supervisor", {}).items() if v}
if sup:
    out += ["", "supervisor {"] + [f"  {k} = {json.dumps(v)}" for k, v in sup.items()] + ["}"]
def listblock(title, src, keys):
    vals = {k: v for k, v in src.items() if v}
    if not vals: return
    out.append(""); out.append(title + " {")
    for k, v in vals.items():
        if isinstance(v, dict): out.append(f"  {k} = {{ " + ", ".join(f"{a} = {json.dumps(b)}" for a, b in v.items()) + " }")
        else: out.append(f"  {k} = {json.dumps(v)}")
    out.append("}")
listblock("sandbox_extra", cfg.get("sandbox-extra", {}), None)
listblock("container_extra", cfg.get("container-extra", {}), None)
rt = os.path.join(d, "runtime.hcl")
if os.path.exists(rt):
    out += ["", "// --- from runtime.hcl ---", open(rt).read().rstrip()]
open(os.path.join(d, "ateam.hcl"), "w").write("\n".join(out) + "\n")
old_id = p.get("name", "")
print(json.dumps({"name": name, "old_id": old_id.replace("_", "__").replace("/", "_")}))
PY
    fi
}

for root in "${projects[@]:-}"; do
    [ -z "$root" ] && continue
    info="$(convert_project "$root" | tail -1)"
    dir="$root/.ateam"
    if [ -f "$dir/config.toml" ]; then
        name="$(printf '%s' "$info" | python3 -c 'import sys,json; print(json.load(sys.stdin)["name"])')"
        old_id="$(printf '%s' "$info" | python3 -c 'import sys,json; print(json.load(sys.stdin)["old_id"])')"
        mv "$dir/config.toml" "$dir/cache/migrated/config.toml"
        [ -f "$dir/runtime.hcl" ] && mv "$dir/runtime.hcl" "$dir/cache/migrated/runtime.hcl"
        if [ -f "$dir/state.sqlite" ] && command -v sqlite3 >/dev/null; then
            sqlite3 "$dir/state.sqlite" "UPDATE agent_execs SET project_id='$name' WHERE project_id='$old_id' OR project_id='';" || true
        fi
    fi
done

# --- retire the old org dir, then register ----------------------------------
if [ -d "$old" ]; then
    say "retiring $old -> $old.migrated (delete it once you are happy)"
    mv "$old" "$old.migrated"
fi
for root in "${projects[@]:-}"; do
    [ -z "$root" ] && continue
    [ -d "$root/.ateam" ] || continue
    say "register: $root"
    (cd "$root" && "$ateam" project-rename) || say "  (registration failed; run 'ateam project-rename' there once the config is fixed)"
done
say "done. Next: 'ateam env' in a project, then 'ateam login' if the agent config dir moved."
