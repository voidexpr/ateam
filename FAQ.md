# FAQ

If you don't find your answer here, see [README.md](README.md) or open an issue.

## What is ateam good for really ?

* Tired of repeating the same generic instructions ?
    * review code for logical issues and structure issues
    * update docs
    * write tests
    * make sure there are no security vulnerability
    * manage potentially deprecated dependencies

You can create skills for some of these but in reality you want to run all of them and prioritize them to regularly and consistently improve the project. This is where ateam comes in.

Another clear scenario to use ateam is as more code is written by agents and only modified by agents there aren't many reasons for humans to review this code or care about the project artifacts. Instead it's best to have dedicated agents constantly improve the project following known good practices. This can be prompted once and automated. This is what ateam is.

Lastly you can use `ateam exec` and `ateam parallel` to create your own mini agent workflow through simple bash script chaining without having to deal with complex frameworks. For example:

```bash
ateam exec @./my_saved_prompt_to_decide_what_todo.md && ateam parallel --prompt "take care of problem 1" --prompt "take care of problem 2" --prompt "take care of problem 3" && ateam exec "verify what the documents produced by the previous step describe and take further action"
```

You can easily swap claude/codex

## How to troubleshoot?

* `ateam env` — paths, roles, and available reports
* `ateam ps` — current and past agent runs
* `ateam tail` — live-stream agent logs
* `ateam inspect` — full command args and logs (use `--auto-debug` for self-diagnosis)
* `ateam cat` — pretty-print agent output (.jsonl)

Use `--help` on any command for details. See [COMMANDS.md](COMMANDS.md) for more.

You can also resume sessions from role agents via `ateam resume <id>` (or `ateam resume --last`), which prints the agent's native resume command and, with `--launch`, exec's into it. Supported for `claude`, `codex`, and `codex-tmux` runs. Override the binary with `ATEAM_RESUME_CLAUDE_CMD` / `ATEAM_RESUME_CODEX_CMD` (see [COMMANDS.md](COMMANDS.md#ateam-resume-exec_id)). Start with `ateam ps`, `ateam inspect`, `ateam cat` first.

## How are agents executed by default?

Both Claude and Codex use their built-in sandbox mode. For Claude this means OS-level restrictions (Seatbelt/bubblewrap) limiting filesystem and network access.

It can easily be changed in `.ateam/config.toml` to select specific profiles and how to run agents is fully customizable in `.ateamorg/runtime.hcl`.

## How to look at the exact prompts used by ateam

Use the `ateam prompt --role ROLENAME --action report` to show the exact prompt used taking into account overloaded and extra prompts added.

## Why not just /code-review (aka /simplify) in claude ?

`/code-review` only looks at potential bugs and code refactoring and is great to use, ateam can look at many other aspects: testing, documentation, etc ...

It actually fits very well as a first step before a full ateam cycle:

```bash
ateam exec "/code-review high for recent commits" && git commit . -m "/code-review" && ateam all
```

## What if I only want to do some of the code changes or only run some of the reports ?

* you can easily select which reports to run with `ateam report --roles ROLE1,ROLE2`
* you can instruct the supervisor: `ateam review --extra-prompt "I only want tasks from code.structure and test.gaps"`

## What if I want to use ateam in a slightly different workflow than report-review-code-verify ?

The `ateam exec` command is a wrapper around coding to run one-shot, unattended prompts. You can use it to build your own automated scripts. It can also be run outside of an ateam project (but requires an ateam organization which is created by default in `$HOME`). You still benefit from ateam observability features:
* `ateam ps` to see current and past execution
* `ateam tail` to see logs in real time
* `ateam cost` to get a token cost report

You can then use `ateam exec` in your own scripts and build your own workflows reusing agent/container management without the ateam prompt/artifact part. It can be ran without an ateam project but does require an ateam org (which is created in $HOME by default).

For example: `ateam exec "/simplify my last few commits" && git commit . -m "round of simplify" && ateam exec "Identify and code at most 5 code refactoring opportunities focused on performance and security. Make sure to commit each separately as soon as they are completed, do run tests between each and fix any issue introduced" --profile docker` and then go get a nice walk outside or valuable family time while your agent is at work. You shouldn't come back to see that it got stuck asking for a bash command approval at the first step.

## What size of project is it for ?

ATeam should be adaptable for projects of many size by running on the entire repo for small to medium projects and have separate ateam projects for various component of bigger projects using a mono repo.
