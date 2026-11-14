# ccmon

Delegation monitor for Claude Code: a Fable orchestrator with pinned subagents
(explorer on Haiku, coder on Sonnet, reviewer on Opus), and a CLI that reports
tokens, dispatches and re-dispatches from session transcripts.

- `.claude/agents/` - the three subagents
- `CLAUDE.md` - delegation rules for the main session
- `docs/MONITOR_BRIEF.md` - build brief for `ccmon`

## Start
```
claude --model fable
```
Enter plan mode and say: "Read docs/MONITOR_BRIEF.md. Plan it, then build it
using the explorer, coder and reviewer agents."

## ccmon usage
Read-only CLI over `~/.claude/projects` transcripts (Python 3.11+, stdlib only).
```
bin/ccmon sessions [--since 7d]     # sessions grouped by repository and task, with totals
bin/ccmon session <id-prefix>       # totals by model/agent, warnings, dispatch timeline
bin/ccmon agents [--since 7d]       # per agent type: tokens, p50/p90, repeats, WARN
bin/ccmon agents --repo ccmon --task ci-workflow   # tokens by agent and model for one task
```
`sessions` and `agents` accept `--repo NAME` and `--task NAME` (exact match).
Global options: `--json` (one JSON document on stdout), `--projects-dir`,
`--agents-dir` (agent definitions used for the model WARN), `--repeat-threshold`
(Jaccard, default 0.5). `--since` takes `Nd`, `Nh`, `Nw` or an ISO date.
Exit codes: 0 ok, 2 usage error (unknown or ambiguous session id, bad option).
Flags in the timeline: `repeat`, `review-loop`, `rechecked`, `WARN`, plus the
status when not `completed` (`running`, `no-run`, `orphan`).

Grouping: a task is a git worktree a session worked in. A session belongs to one
repository and at most one task: the last worktree it entered (from the
`worktree-state` lines of its main transcript, ignoring `agent-<hex>` subagent
worktrees), else its first `cwd`; the repository is the path before
`/.claude/worktrees/`. A session that switched worktrees is counted whole under the
last one; its `worktrees` field lists every task it entered. In `--json` each session
gains `repo`, `repo_path`, `task`, `worktrees`, and `sessions --json` gains `repos`
(repository and task totals with the same keys as a session). Known limits: a session
started in a subdirectory of a repository, outside any worktree, shows that
subdirectory as its repository; review loops are detected inside one session, so a
task's count is the sum over its sessions.

For a resumed agent (`SendMessage`), duration covers dispatch to first return while
tokens include the resumed work. `running` means no completion (hand-back or
task-notification) was found in the transcript.

Tests: `python3 -m unittest discover -s tests -v`

## Live dashboard
`ccmon-tui` is a terminal dashboard over the same data (Go, Bubble Tea v2). It
shells out to `python3 -m ccmon ... --json`, so the numbers always match the CLI,
and it never writes under `~/.claude`.
```
make install    # check Go and Python, fetch modules, build bin/ccmon-tui
make up         # build and run
make test       # Python unittest + go vet + go test
make snapshot VIEW=session SIZE=140x40 ARGS="--session 04ca0148 --no-color"
```
Requirements: Go 1.27+, Python 3.11+ on `PATH`. The binary finds the repo
relative to its own path (`CCMON_ROOT` overrides) and works from any directory;
`--projects-dir` and `--agents-dir` are resolved against your working directory
and passed through to the CLI.

Views: dashboard (KPI tiles, sessions table grouped by repository and task with per-group totals, agent and model mix, Now running),
session (header signals, timeline with a row per concurrent dispatch, dispatch
list, detail pane), agents (per agent type token classes and p50/p90). A session
is live when its transcript changed in the last 30 s. One 1 s poll of the
transcripts' mtimes finds changed, new and deleted sessions; only those are
re-read. Quitting stops every python process the dashboard started.

Keys: `tab`/`shift+tab` or `1 2 3` views, `j k g G` or arrows to move,
`enter` open, `esc`/`backspace` back, `r` range (24h 7d 30d all; retry on the
error screen), `f` follow the latest session, `s` sort, `/` filter, `?` help,
`q` quit. `NO_COLOR` or `--no-color` gives a plain rendering.

`make screenshot VIEW=session ARGS="--session <id>"` renders a colored frame to `shots/` as a PNG (needs Chrome; `make install` reports whether it is there).

Snapshot mode renders one settled frame without a TTY:
`bin/ccmon-tui --snapshot dashboard|session|agents|help --size 140x40
[--session <id-prefix>] [--range 7d] [--no-color]`.

## Factory
`factory/` is a Claude Code plugin: a dispatcher session hands each task to a lead session running in its own git worktree, and the lead plans, builds through subagents (`factory:explorer`, `factory:coder`, `factory:reviewer`) and opens a draft pull request. Rules are in `docs/specs/factory.md`.

Install for development: `claude --plugin-dir factory`, or through the marketplace file at the repo root: `/plugin marketplace add <path-to-this-repo>`, then `/plugin install factory@factory`. Start the dispatcher with `/factory:dispatch`.

The `factory` command (the plugin's `bin/`, on the Bash tool's `PATH` while the plugin is loaded; Python 3.11+, run from inside a repo):
```
factory new <slug> (--brief <text> | --brief-file <path>) [--dispatcher <session-name>] [--base <slug>]
factory status [--json]
factory clean [--dry-run]
factory state working|blocked|handed-over [<deliverable>]   # a lead declares its task's state; a link, or a file outside the worktree
factory ack <slug> acknowledged|dismissed       # the dispatcher, on the human's word, for handed-over work
factory retro                                   # a lead's retrospective, JSON on stdin
factory feedback [--json] [--status open|accepted|rejected|done]
factory feedback add --role <role> [--slug <slug>] [--model <model>]   # items on stdin
factory feedback set <id> open|accepted|rejected|done [--pr <url>]
```
`new` starts a lead in `.claude/worktrees/<slug>`; `--base` stacks it on another open task's branch. The calling session must be named `--dispatcher` (default `dispatcher-<repo directory name>`); name it with `/rename <name>`, or `new` refuses. `new` refuses when the repo is at its cap of tasks in motion or blocked on the human's answer; a task stops counting once its lead has handed its work over (`state handed-over`, or an open pull request for a task that never declared) and no session is unfinished in it (an open interactive session counts as unfinished), so work waiting for review does not block new work. `status` shows one row per task with its phase (`motion`, `blocked`, `review`, `merged`, `settled`), declared state, session, changes, distance from the default branch, pull request and staleness, then the number in motion against the cap (and how many of those are blocked), the number waiting for review, merged and settled and not yet cleaned, and a `waiting on the human` line for each task in review; `clean` removes a task only on positive evidence (pull request merged into the default branch, or the human acknowledged or dismissed its handed-over work and its head is pushed; nothing uncommitted, no live session, nothing stacked on it), removes nothing when sessions cannot be listed, and says why it kept the rest.

The factory records what happens in four JSONL files under `~/.claude/factory/`: `events.jsonl` (the plugin's hooks append to it automatically, and `new` and `clean` add task events), `retros.jsonl` (lead retrospectives from `factory retro`), `feedback.jsonl` (the "Factory feedback" items from reports, with their status) and `tasks.jsonl` (each task's declared state, from `new`, `state` and `ack`). Formats are in `docs/specs/factory-events.md`; `tasks.jsonl` is in `docs/specs/factory.md`.

Exit codes: 0 success, 2 usage or precondition error, 1 a `claude`, `git` or `gh` step failed.

Settings, all optional, in `~/.claude/factory/settings.json`: `worktree_cap` (default 5 tasks in motion per repo), `lead_model` (default `fable`), `lead_permission_mode` (default `auto`), `task_time_limit_hours` (default 2). A settings file that is not valid JSON is an error.

Tests: `make test-factory`.
