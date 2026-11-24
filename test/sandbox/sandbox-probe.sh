#!/bin/sh
# Runs INSIDE a sandbox started by run-probes.sh and checks what the sandbox
# allows and refuses. Every write goes to the scratch folder run-probes.sh
# made; the only path of yours it touches is `ls ~/.ssh`, which reads.
#
# Usage (from run-probes.sh): sandbox-probe.sh SUITE
#   base         the rules every profile gets
#   carve        a path opened for reading inside an excluded folder
#   carve-write  a path opened for writing inside an excluded folder
# Environment: PROBE_ROOT (scratch folder), PROBE_NET (1: check hosts),
# PROBE_SSH (1: ~/.ssh exists).
#
# Prints one PASS or FAIL line per check and exits 1 on any FAIL.
#
# What a write did to the real files is checked afterwards by run-probes.sh,
# from outside: on Linux an excluded folder is replaced by an empty one, and
# inside a writable folder a write to it succeeds and is thrown away.

# The commands are run by `sh -c`, which expands their variables.
# shellcheck disable=SC2016

suite=${1:-base}
R=${PROBE_ROOT:?PROBE_ROOT is not set}
export R

pass=0
fail=0

# allowed DESCRIPTION SHELL-COMMAND: the command must succeed.
# On a failure the last line the command printed is shown: its own reason.
allowed() {
    if out=$(sh -c "$2" 2>&1); then
        pass=$((pass + 1))
        echo "PASS allowed: $1"
    else
        fail=$((fail + 1))
        echo "FAIL must be allowed: $1    [$2]"
        [ -n "$out" ] && echo "     $(printf '%s\n' "$out" | tail -1)"
    fi
}

# refused DESCRIPTION SHELL-COMMAND [UNDO]: the command must fail. UNDO puts
# things back when it did not, so the checks after it still mean something.
refused() {
    if sh -c "$2" >/dev/null 2>&1; then
        fail=$((fail + 1))
        echo "FAIL must be refused: $1    [$2]"
        [ -n "${3:-}" ] && sh -c "$3" >/dev/null 2>&1
    else
        pass=$((pass + 1))
        echo "PASS refused: $1"
    fi
}

base() {
    allowed "ATEAM_IN_SANDBOX=1 is set" '[ "$ATEAM_IN_SANDBOX" = 1 ]'

    # the working folder and the write grant
    allowed "write in the working folder" 'echo x > probe-made.txt'
    allowed "write under a --write path" 'echo x > "$R/extra/new.txt"'
    allowed "read a file of the home outside the working folder" 'cat "$R/outside/readable.txt"'
    refused "create a file outside the working folder" 'echo x > "$R/outside/new.txt"'
    refused "append to a file outside the working folder" 'echo x >> "$R/outside/readable.txt"'

    # an excluded folder outside the working folder
    refused "read a file of an excluded folder" 'cat "$R/secret/token"'
    refused "see the entries of an excluded folder" 'ls "$R/secret" | grep -q .'
    refused "create a file in an excluded folder" 'echo x > "$R/secret/new.txt"'

    # an excluded folder inside the working folder: closed for writes too
    # the writes are tried here and judged from outside (see the top)
    refused "read an excluded file inside the working folder" 'cat private/token'
    (echo x >private/token) 2>/dev/null
    (echo x >private/new.txt) 2>/dev/null
    refused "rename an excluded folder inside the working folder" 'mv private private2' 'mv private2 private'

    # ateam's own files
    refused "read the user's ateam directory" 'cat "$R/ahome/secrets.env"'
    refused "read the project's ateam.hcl" 'cat .ateam/ateam.hcl'
    refused "write the project's ateam.hcl" 'echo "# probe" >> .ateam/ateam.hcl'
    refused "create a profile in the project's .ateam" 'mkdir -p .ateam/profiles && echo x > .ateam/profiles/probe.hcl'
    allowed "write in the project's .ateam/shared" 'mkdir -p .ateam/shared && echo x > .ateam/shared/probe.txt'

    # The trailing slash asks for the content: when ~/.ssh is a link whose
    # target is closed, plain `ls ~/.ssh` prints the link's name and succeeds.
    if [ "${PROBE_SSH:-0}" = 1 ]; then
        refused "see the entries of ~/.ssh" 'ls "$HOME/.ssh/" | grep -q .'
    else
        echo "SKIP see the entries of ~/.ssh: it does not exist"
    fi

    # git: usable, with its config and hooks read-only
    allowed "git status" 'git status'
    refused "append to .git/config" 'echo "# probe" >> .git/config'
    refused "create a git hook" 'echo x > .git/hooks/probe'
    refused "rename .git away" 'mv .git .git2' 'mv .git2 .git'
    allowed "git add" 'git add probe-made.txt'
    allowed "git commit" 'git -c user.name=probe -c user.email=probe@example.invalid -c commit.gpgsign=false commit -q -m probe'

    if [ "${PROBE_NET:-1}" != 1 ]; then
        echo "SKIP hosts: PROBE_NET is not 1"
    elif ! command -v curl >/dev/null 2>&1; then
        echo "SKIP hosts: curl is not installed"
    else
        allowed "reach a host of --domain (example.com)" 'curl -sS -m 20 -o /dev/null https://example.com'
        refused "reach another host (example.org)" 'curl -sS -m 20 -o /dev/null https://example.org'
    fi
}

carve() {
    # a read opened inside an excluded folder
    allowed "read a file opened by --read inside an excluded folder" 'cat "$R/secret/open.txt"'
    refused "write that file: it is outside every writable folder" 'echo x >> "$R/secret/open.txt"'
    refused "read the rest of the excluded folder" 'cat "$R/secret/token"'
    # Linux lists the folder with the opened entry alone; macOS refuses the listing.
    refused "see the other entries of the excluded folder" 'ls "$R/secret" | grep -q token'

    # the same inside the working folder: reopened for reading stays read-only
    allowed "read a file opened by --read inside an excluded folder of the working folder" 'cat private/readme.txt'
    refused "write that file: read-only" 'echo x >> private/readme.txt'
    refused "read the rest of that folder" 'cat private/token'
    refused "rename the folder above the read-only file" 'mv private private2' 'mv private2 private'
}

carve_write() {
    # a write opened inside an excluded folder
    allowed "write under a --write path inside an excluded folder" 'echo x > "$R/secret/rw/new.txt"'
    allowed "remove a file there" 'rm "$R/secret/rw/new.txt"'
    refused "read the rest of the excluded folder" 'cat "$R/secret/token"'
}

case "$suite" in
base) base ;;
carve) carve ;;
carve-write) carve_write ;;
*)
    echo "unknown suite: $suite" >&2
    exit 2
    ;;
esac

echo "$suite: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
