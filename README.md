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
bin/ccmon sessions [--since 7d]     # one row per session
bin/ccmon session <id-prefix>       # totals by model/agent, warnings, dispatch timeline
bin/ccmon agents [--since 7d]       # per agent type: tokens, p50/p90, repeats, WARN
```
Global options: `--json` (one JSON document on stdout), `--projects-dir`,
`--agents-dir` (agent definitions used for the model WARN), `--repeat-threshold`
(Jaccard, default 0.5). `--since` takes `Nd`, `Nh`, `Nw` or an ISO date.
Exit codes: 0 ok, 2 usage error (unknown or ambiguous session id, bad option).
Flags in the timeline: `repeat`, `review-loop`, `rechecked`, `WARN`, plus the
status when not `completed` (`running`, `no-run`, `orphan`).

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

Views: dashboard (KPI tiles, sessions table, agent and model mix, Now running),
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
