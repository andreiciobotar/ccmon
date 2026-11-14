# isshoni reference

Read-only CLI over `~/.claude/projects` transcripts (Python 3.11+, stdlib only).
```
bin/isshoni sessions [--since 7d]     # sessions grouped by repository and task, with totals
bin/isshoni session <id-prefix>       # totals by model/agent, warnings, dispatch timeline
bin/isshoni agents [--since 7d]       # per agent type: tokens, p50/p90, repeats, WARN
bin/isshoni agents --repo isshoni --task ci-workflow   # tokens by agent and model for one task
```
`sessions` and `agents` accept `--repo NAME` and `--task NAME` (exact match).
Global options: `--json` (one JSON document on stdout), `--projects-dir`,
`--agents-dir` (agent definitions used for the model WARN), `--prices` (price file), `--repeat-threshold`
(Jaccard, default 0.5). `--since` takes `Nd`, `Nh`, `Nw` or an ISO date.
Exit codes: 0 ok, 2 usage error (unknown or ambiguous session id, bad option).
Flags in the timeline: `repeat`, `review-loop`, `rechecked`, `WARN`, plus the
status when not `completed` (`running`, `no-run`, `orphan`).

Cost: every command shows money next to tokens, priced per message from
`isshoni/prices.toml` (per million tokens: input, output, cache write 5m and 1h, cache
read; an optional `long_prompt` table for a model that charges more above a prompt
size). Edit the file or pass `--prices PATH` (`isshoni-tui --prices` passes it on); a
missing or invalid file exits 2. A model id matches a table exactly or minus a trailing
`-YYYYMMDD`. A model with no table is never counted as zero: it shows as `unpriced`,
figures that contain its tokens end in `*`, and a footer names it. In `--json` every
usage object gains `cost` and `unpriced` (tokens), each session, task and repository a
`cost` block beside `tokens` (with `unpriced_tokens`, `unpriced_models`), agents
`cost_p50`/`cost_p90`, and each document a `prices` block. Limits: cost is the list
price at the file's `as_of` date, not what a subscription charges; fast mode, US-only
inference, web search and batch are not priced; it is a lower bound, because Claude
Code makes helper calls (web search and fetch summaries) that it does not write to the
transcript.

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

Plugin agents: a plugin's agent is dispatched as `<plugin>:<name>` (`factory:coder`)
and counts as `<name>` for review loops, re-checks and repeats, and for its colour and
position in the dashboard. Its full name is shown, and it keeps its own row in `agents`
and its own `by_agent` key, apart from a bare `coder`. Its model is not checked by
default, because a transcript does not record where a plugin's definitions were loaded
from; pass `--agents-dir <plugin>/agents` to check it against `<name>.md` there (the option
replaces the default directories, so bare agents are checked against the same files).

For a resumed agent (`SendMessage`), duration covers dispatch to first return while
tokens include the resumed work. `running` means no completion (hand-back or
task-notification) was found in the transcript.

## Dashboard (`isshoni-tui`)

`make up` builds and runs it. It shows what `isshoni --json` reports, re-reading a
session when its transcript changes (checked every second). The only file it writes
under `~/.claude` is the factory's settings, when you save the cap. It finds the
Python package from `ISSHONI_ROOT`, else from the binary's own location. It needs a
terminal of at least 60x16.
```
bin/isshoni-tui [--range 24h|7d|30d|all] [--follow] [--no-color]
                [--projects-dir DIR] [--agents-dir DIR] [--prices PATH]
bin/isshoni-tui --snapshot dashboard|session|agents|factory|help [--size 140x40] [--session ID] [--row N]
```
`--range` defaults to `7d`. `NO_COLOR` gives the plain rendering too. `--snapshot`
prints one settled frame and exits, non-zero when the load fails; `--row N` puts the
dashboard cursor on table row N (0 is the `all` row); `make screenshot
VIEW=... SIZE=...` turns it into a PNG under `shots/` (needs Chrome).

| Key | |
|---|---|
| `tab` `shift+tab`, `1` `2` `3` `4` | switch view: dashboard, session, agents, factory |
| `↑` `↓` `j` `k`, `g` `G`, `pgup` `pgdn` | move, first and last, page |
| `enter`, `esc` | open a session row (nothing on a group row), back |
| `r` | range: 24h, 7d, 30d, all (on the error screen: retry) |
| `s` | sort: recent, cost, subagent share |
| `/` | filter by `repo`, `repo/task`, project or session id |
| `f` | follow the most recently active session |
| `c` | factory view: edit the cap (digits or `↑` `↓`, `enter` saves, `esc` cancels) |
| `?`, `q` | help, quit |

- **Dashboard:** totals for the range, the sessions as a tree of repository, task and
  session, the agent and model mix, and every dispatch running now. A group row sums
  its sessions (rates from summed counts, never a mean of rates); a group of one
  session is a single row. The top row, `all`, sums every session shown. The cursor
  moves over every row; the agent and model mix show the row under it and name it in
  their titles, with money and share over that row's cost. The tiles always total every
  session shown, and a line under them says so. In the mix a plugin's agent counts with
  its role (`factory:coder` as `coder`); the Agents view keeps them apart. A cursor row
  that disappears (filtered out, merged away) falls back to `all`. Columns: time (last activity when sorted by
  recent, else start), `COST`, `TOKENS`, `MAIN│SUB` and `SUB%` (shares of cost), `DISP`
  (dispatches), `REP` (repeats), `LOOP` (review loops), `RCHK` (re-checks), `WARN`
  (model warnings), `BAD` (malformed lines, only when there are any). A narrow
  terminal drops `BAD`, `SUB%`, `TOKENS`, then the time.
- **Session:** cost by agent, a timeline with a lane per agent type (dispatches of one
  type that overlap stack on extra rows), the dispatch list and the selected dispatch
  in detail. Idle stretches of the timeline collapse to a marker labelled with their
  length. A dispatch still `running` in a session that is no longer live is labelled
  `unfinished`.
- **Agents:** per agent type: dispatches, models, tokens and cost, p50/p90 cost and
  durations, repeats, review loops, warnings.
- **Factory:** for each repository seen in the range that has task worktrees, what
  `factory status --json` reports: the tasks holding a slot against the cap, and every
  task with its phase, elapsed time, pull request or deliverable, base, and `overtime`
  or `stale`. It refreshes every 30 seconds while shown. `c` sets `worktree_cap` (1 to
  999) in `~/.claude/factory/settings.json`, leaving the other keys as they were; a file
  that is not a JSON object is never overwritten. See [factory.md](factory.md).

A session is live when its transcript changed in the last 30 seconds or so. Agents keep
one colour and one order everywhere: main, coder, reviewer, explorer, then the rest.
