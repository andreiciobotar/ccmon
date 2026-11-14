# Build brief: delegation monitor

Paste this into a Fable session (`/model fable`, plan mode) and say:
"Plan this, then build it using the explorer, coder and reviewer agents."

## Goal
A CLI tool, `isshoni`, that reads Claude Code session transcripts and reports how
the main session delegates to subagents, so we can see if the Fable-orchestrator
setup is efficient.

## Data sources
1. **Transcripts (primary):** JSONL files under `~/.claude/projects/<project>/`.
   Subagent runs are recorded too (either inline as sidechain messages or as
   separate files near the session; the format depends on the Claude Code version).
2. **Hook log (optional, live):** a `PreToolUse` hook on the subagent tool and a
   `SubagentStop` hook append one JSON line per event to `~/.claude/isshoni/events.jsonl`.

**Step 0 is mandatory: have the explorer inspect real transcript files and report
the actual schema** (field names for model, usage, tool calls, sidechain/subagent
linkage, message ids, timestamps) with excerpts. Do not assume the format.
Things to confirm specifically:
- The name of the subagent tool in tool_use blocks (`Task` or `Agent`) and its
  input fields (`subagent_type`, `description`, `prompt`, optional `model`).
- How a subagent's messages link back to the dispatching tool_use.
- Whether one API response is split over several lines that repeat the same
  `usage` (if so, dedupe by message id or tokens get double-counted).

## Metrics (per session, and aggregated over a date range)
Tokens:
- Input, output, cache-write, cache-read tokens, broken down by **model** and
  by **agent** (main session vs each subagent type).
- Share of total tokens spent by the main session vs subagents
  (the setup works if subagents carry most of the volume).

Dispatches:
- Number of dispatches per agent type.
- Tokens and duration per dispatch (p50 / p90).
- Models actually used per agent type, and a **WARN** whenever it differs from the
  `model:` in `.claude/agents/<name>.md` (catches CLAUDE_CODE_SUBAGENT_MODEL and
  inheritance mistakes).

Re-dispatches (the signal-quality metric):
- **Repeat:** same agent type dispatched again in the same session with a similar
  prompt (token-set Jaccard >= 0.5 on the description+prompt; make the threshold
  a flag). Suggests the first report lacked what Fable needed.
- **Review loop:** a coder dispatch that follows a reviewer dispatch whose result
  contains `CHANGES_NEEDED`. Count loops per task.
- **Fable re-checked:** after an explorer returns, the main session Reads one of
  the `path:line` files it cited. Healthy in moderation; record the rate.

## Output
- `isshoni sessions [--since 7d]`: one row per session with the totals above.
- `isshoni session <id>`: a timeline of dispatches: time, agent, model, tokens in/out,
  duration, re-dispatch flags, first line of the description.
- `isshoni agents [--since 7d]`: per-agent-type aggregates.
- `--json` on every command for piping into other tools.

## Constraints
- Python 3.11+, standard library only (no installs).
- Read-only: never modify transcripts.
- Tolerate malformed or unknown lines (count and report them, don't crash).
- Include a small fixture transcript in `tests/` and tests for the token dedupe,
  the dispatch linkage and both re-dispatch detectors.

## Done when
- Running `isshoni session <id>` on the session that built this tool shows its own
  explorer/coder/reviewer dispatches with sensible numbers.
- The reviewer returns APPROVE.
