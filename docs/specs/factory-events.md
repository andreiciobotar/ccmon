# Spec: factory telemetry, collection side

Implements the "Collect" section of `docs/specs/telemetry.md` except pull request outcomes from GitHub (a reader can get them with `gh`; nothing needs recording). No dashboard, no cost weighting, no kaizen review.

## Outcome
A finished task leaves three kinds of record under `~/.claude/factory/`, which survives the worktree:
- `events`: one line per event, written by hooks the plugin ships and by `factory new` / `factory clean`.
- `retros`: one line per lead retrospective, written by `factory retro`.
- `feedback`: one line per "Factory feedback" item and one per status change, written by the hooks, `factory feedback add` and `factory feedback set`.

Each is a log of JSONL files, one per month (see "Month files and what is kept").

Every record carries `ts` (UTC, `2026-11-14T00:01:02Z`), `repo`, `slug`, `role`, `model`, `version`, `session`.

## Facts this rests on
Measured on Claude Code 2.1.325 with three probe sessions (`claude --bg --plugin-dir <probe plugin> [--plugin-dir factory]`, every hook dumping its payload), plus the transcripts of session `fa1fc39c` (task `ci-workflow`) and the hooks reference (code.claude.com/docs/en/hooks).

1. A plugin's `hooks/hooks.json` runs in a background session started with `--plugin-dir`: 20 hook calls recorded across the three probes. Session `fa1fc39c` (a real lead, `--bg --worktree --plugin-dir`) shows 7 `stop_hook_summary` lines from another plugin's hook, so plugin hooks also run with `--worktree`.
2. Observed firing, with payload fields beyond the common `session_id`, `transcript_path`, `cwd`, `hook_event_name`:
   - `SessionStart`: `source`, `model`.
   - `SessionEnd`: `reason` (`other` on `claude stop`).
   - `UserPromptSubmit`: `prompt`. Fires for the first prompt, for every subagent hand-back (`<agent-message from=...`) and for a cross-session message (the dispatcher's answer in the end-to-end run, 7 seconds after the `QUESTION:`).
   - `Stop`: `last_assistant_message`, `background_tasks`.
   - `SubagentStart`: `agent_id`, `agent_type` (`factory:explorer`).
   - `SubagentStop`: `agent_id`, `agent_type`, `agent_transcript_path`. **No `last_assistant_message`** when the subagent delivered its report with the `SubagentHandback` tool.
   - `PostToolUse` for `SubagentHandback`, fired inside the subagent: `agent_id`, `agent_type`, `tool_input.message` holding the whole report, and the parent's `session_id`, `transcript_path`, `cwd`.
   - `PostToolUse` for `SendMessage`: `tool_input.to`, `tool_input.message`.
   - `PermissionRequest` (`tool_name`) then `Notification` (`notification_type: permission_prompt`, `message`) in a `manual`-mode background session that needed a permission.
3. Full count over `fa1fc39c`: 2 subagents, 6 reports, all 6 delivered through `SubagentHandback`, none as a closing text message. So feedback is filed from the `SubagentHandback` hook. In the end-to-end run a subagent first ended with a text report (`SubagentStop` carried it as `last_assistant_message`), then handed back a reworded copy five seconds later; filing from both gave two near-identical entries, so `SubagentStop` is not a feedback source.
4. Not observed: `PermissionDenied`. The reference says it fires only for denials by the auto-mode classifier; the one command tried was allowed. It is registered on the reference's word.
5. Not usable: `WorktreeCreate`. The reference: "Configuring a WorktreeCreate hook replaces that default git behavior." `WorktreeRemove` is not registered either: the factory removes worktrees itself. `factory new` and `factory clean` record `TaskStarted` and `TaskRemoved` instead.
6. A hook's environment has `CLAUDE_PROJECT_DIR` (the session's directory), `CLAUDE_PLUGIN_ROOT`, `CLAUDE_CODE_SESSION_ID`. The Bash tool's shell has `CLAUDE_CODE_SESSION_ID` but not `CLAUDE_PROJECT_DIR`. A variable exported by the launcher did not reach the hooks, so `factory new` cannot pass the slug through the environment.
7. The transcripts of `fa1fc39c` record `cwd` as `<repo>/.claude/worktrees/ci-workflow` on all 185 lines that have one: a lead's directory names its repo and slug.
8. A subagent's transcript is `<transcript_path without .jsonl>/subagents/agent-<agent_id>.jsonl` (both subagents of `fa1fc39c`, and the probe's `agent_transcript_path`). Every assistant line carries `message.model` (79 of 79 in the lead, 57 and 23 in the subagents); the `.meta.json` beside it names the wrong model (`pickedEffortModel` said fable for a Haiku and an Opus run), so it is not used.
9. Non-zero hook exits other than 2 do not block; `SessionEnd` hooks get 1.5 seconds in total.

## Design
Everything lives in `factory/bin/factory` (one owner, already on the Bash tool's `PATH`), plus `factory/hooks/hooks.json`.

### Tags
- `repo`, `slug`: from the session directory (`CLAUDE_PROJECT_DIR`, else the payload's `cwd`, else the process's working directory for the commands). A path matching `<repo>/.claude/worktrees/<name>[/...]` gives `repo` and, unless `<name>` starts with `agent-`, `slug`. Any other path is its own `repo` with `slug` null. No `git` call.
- `role`: the payload's `agent_type` without the `factory:` prefix when present; else `subagent` when the payload has an `agent_id` (the end-to-end run showed one `SubagentStop` with no type); else `lead` when there is a slug; else `other` (the dispatcher and any other session that loads the plugin).
- `model`: the payload's `model` when present; else the last `"model":"..."` in the final 64 KiB of the transcript, the subagent's (fact 8) when the payload has `agent_id`, the session's otherwise; null when there is none. The commands find the session transcript as `~/.claude/projects/*/<CLAUDE_CODE_SESSION_ID>.jsonl`. Transcripts are only read.
- `version`: `version` from the plugin's `.claude-plugin/plugin.json`. A pull request that changes the rules bumps it; this one sets `0.2.0`.
- `session`: the payload's `session_id`, or `CLAUDE_CODE_SESSION_ID`.

### Store
`STORE = Path("~/.claude/factory")`. One function appends one JSON line to `<store>/<name>.jsonl` with a single `os.write` on a descriptor opened `O_WRONLY | O_APPEND | O_CREAT` (mode 0600), creating the directory when missing. It raises `OSError` on failure; the hook swallows it, the commands report it.

#### Month files and what is kept (`docs/specs/event-log-bounds.md`)
The three logs are written one file per UTC month, named by the record's own `ts`: `events-2026-11.jsonl`, `retros-2026-11.jsonl`, `feedback-2026-11.jsonl`. A file is only ever appended to, or deleted whole; nothing is rewritten in place, so the atomic append is the only thing concurrent writers rely on. The unsuffixed files written before this rule (`events.jsonl`, ...) are read first, as the oldest, and count as the month they were last modified in.

What is kept (12 is the constant `KEEP_MONTHS`; there is no setting):
- **Events:** the current month and the 12 before it. Older files are deleted, not archived.
- **Retrospectives:** everything, forever (one line per task).
- **Feedback:** every entry that is `open` or `accepted`, forever; `done` and `rejected` entries for at least 12 months. A file older than that is deleted, oldest first, after the lines it holds for entries that are still `open` or `accepted` (and any status line a remaining entry depends on) are appended, unchanged, to the current month's file. So a carried line's `ts` is older than its file's month, and feedback is read whole (`factory feedback`), not by time range.

Deleting happens when a log's first record of a new month is written, in that process (a hook or a command), best effort: a failure is ignored and retried next month. So a store nobody writes to is never pruned.

Reading:
```
factory log events|retros [--since <ts>] [--until <ts>]
```
prints the stored records, oldest file first, one JSON object per line. A bound is a `ts` or a prefix of one, at least `YYYY-MM` (`2026-11`, `2026-11-14`, `2026-11-14T09:30:00Z`); a record is printed when `since <= ts < until`, compared as strings. Only the month files inside the range are opened. Lines that are not JSON objects are skipped. A bad bound exits 2.

### `factory hook`
Run by every hook. Reads the payload from stdin, appends one event, prints nothing, exits 0 whatever happens (bad JSON, unwritable store, any exception).

Event line: the tags, `event` (the hook's `hook_event_name` verbatim) and these payload fields when present, nothing else:

| event | fields copied |
|---|---|
| `SessionStart` | `source` |
| `SessionEnd` | `reason` |
| `UserPromptSubmit` | `from`: `subagent` when the prompt starts with `<agent-message`, `session` for `<cross-session-message`, `notification` for `<task-notification`, else `human` |
| `Stop` | `background_tasks`: the count of entries |
| `SubagentStart`, `SubagentStop` | `agent_id` |
| `Notification` | `notification_type`, `message` |
| `PermissionRequest` | `tool_name` |
| `PermissionDenied` | `tool_name`, `reason` |
| `PostToolUse` | `tool_name`, `agent_id`; for `SendMessage` also, always, `to` and `kind` (`QUESTION`, `BLOCKED`, `OVERTIME`, `SPLIT` or `DONE` when the message starts with that word and a colon, else null) |

Feedback is filed by the same call from two texts: `tool_input.message` of a `SubagentHandback`, and `tool_input.message` of a `SendMessage` whose `kind` is `DONE`. A payload field of an unexpected type is treated as absent; the event is still recorded.

`hooks.json` registers `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `Stop`, `SubagentStart`, `SubagentStop`, `Notification`, `PermissionRequest`, `PermissionDenied`, and `PostToolUse` with matcher `SendMessage|SubagentHandback`, each as `"${CLAUDE_PLUGIN_ROOT}/bin/factory" hook` with `timeout` 10.

### Feedback
- Section: the text after the last line that, stripped of `#`, `*`, `:` and spaces, equals `factory feedback` (any case), up to the next line starting with `#` or the end. No such line: no feedback.
- Items: a line starting with `- `, `* ` or `<digits>. ` starts an item; other lines continue the current one; a section with no such line is one item. The marker is dropped, the rest is kept verbatim. Lines before the first marker are dropped when there is one. An item that is empty or reads `none` (any case, quotes and trailing punctuation ignored) is not filed, so `none` and `- none` both file nothing.
- Entry line: the tags, `id`, `text`, `status: "open"`. `id` is the first 12 hex digits of the SHA-256 of `repo`, `slug`, `role` and `text` joined by NUL, so the same item filed again (a resumed subagent hands back twice) has the same id.
- Status line: `id`, `ts`, `status`, and `pr` when given. Lines are only ever appended.
- Reading: an entry is the first line with that `id` and a `text`; its status and `pr` come from the status line for the id with the latest `ts` (the later line on a tie; no `pr` on that line: none shown), else `open`. Lines that are not JSON objects are skipped.
- Kind: an entry whose text starts with `DECISION:` is a `decision` (a key decision a lead took without asking: what and why), every other entry an `issue`. The kind is computed on reading and never stored, so entries filed before kinds existed read correctly, and an item of a report's section that starts with `DECISION:` is a decision too.

Commands:
```
factory feedback [--json] [--status open|accepted|rejected|done] [--kind issue|decision]
factory feedback add --role <role> [--slug <slug>] [--model <model>]   # text on stdin
factory feedback decide '<what>. Why: <why>'
factory feedback set <id> open|accepted|rejected|done [--pr <url>]
```
- The listing prints one entry per line: id, status, slug, role, the first 100 characters of the text on one line; `--json` prints the entries as a JSON array with every field plus `kind`. `--kind` and `--status` each narrow the listing; without `--kind` both kinds show.
- `decide` files one decision: whitespace runs become single spaces and the text is stored as `DECISION: <text>`, with the tags of the working directory and session (role `lead` in a task worktree). Empty, or longer than 200 characters: exit 2, nothing filed. It prints the id.
- `add` files what a hook could not: stdin is a report (its section is used) or, without a heading line, the items themselves. Tags come from the working directory and session; `--slug` and `--model` override them. It prints the ids filed, or `no items`.
- `set` takes an id or a unique prefix of one; no match or several is an error (exit 2).

### `factory retro`
Reads one JSON object from stdin, appends it to the `retros` log with the tags. Exactly these keys, or exit 2 naming the problem:
```json
{"defects": [{"caught_by": "tests|reviewer|lead|ci|human", "what": "..."}],
 "slowed": ["..."], "rules_missing": ["..."], "rules_dead_weight": ["..."], "brief_questions": ["..."]}
```
Lists may be empty; strings must be non-empty. Run outside a task worktree (no slug) it refuses.

### Events from the factory's own commands
- `factory new`, after the session started: `TaskStarted` with `repo`, `slug`, `role: "dispatcher"`, `model` null, `session` (the caller's), and `base`, `lead_model`.
- `factory clean`, after each removal: `TaskRemoved` with `repo`, `slug`, `role: "dispatcher"`, `model` null, `session`.
A failed write is ignored; output and exit codes of `new`, `status` and `clean` do not change.

### Errors
`retro`, `feedback add`, `feedback decide` and `feedback set` exit 1 with one line when the store cannot be written, 2 on bad input.

### Rules
- `factory/agents/{explorer,coder,reviewer}.md`: the report format gains a last section `## Factory feedback`: at most three things about the brief, the rules, the tools or permissions that cost time or forced a guess, not about the task; each with what happened, what it cost and a suggested change; `none` otherwise. Filed automatically.
- `factory/roles/lead.md`: the `DONE:` message ends with a line `Factory feedback` and the lead's own items only (subagents' are filed from their reports); before `DONE:` the lead runs `factory retro`.
- `factory/skills/dispatch/SKILL.md`: the critique section names `factory feedback` and the two files.

## Known limits
- A subagent in its own worktree (`isolation: worktree`) may report a `cwd` under `.claude/worktrees/agent-*`; `CLAUDE_PROJECT_DIR` is used first to avoid that, unverified for that case.
- The lead's feedback is filed only from a `DONE:` message that was sent with `SendMessage`; a lead that ends with the message as plain output files it with `factory feedback add`.
- Concurrent appends rely on `O_APPEND` writes of one line being atomic on a local filesystem. Counted on 2026-11-14 over the live store after a day of up to five concurrent leads: 0 malformed lines in 166; a test runs 8 writers against a pruner.
- Within a file, lines are in write order; a reader that needs strict time order across the unsuffixed file and the month files sorts by `ts`. A session still running a plugin copy from before the month files keeps appending to the unsuffixed files; they are read, so nothing is lost, and while one is being written no older month file of that log is deleted.
- A crash between carrying feedback lines and deleting their file, or two sessions pruning at once, leaves duplicate lines; they fold to the same entries.
- `factory feedback` lists carried entries after newer ones.
- A `feedback set` that reopens a closed entry more than 12 months old in the same instant its file is deleted is lost. Two status lines for one entry with the same `ts`, one of them carried, may swap which wins.
- A month file has no size limit of its own: it holds what one month produces (about 280 bytes per event; 30 KB of events in the first ten hours of use).
- A subagent that never calls `SubagentHandback` files nothing; the lead files its items with `factory feedback add`.
- `PermissionDenied`, `TaskStarted` and `TaskRemoved` have not been seen in a live session (the end-to-end lead was started without `factory new`, which requires the dispatcher's session name); the last two are covered by tests.
- A session started with `--plugin-dir` while another copy of the plugin is installed may find the other copy's `factory` first on `PATH` (seen in the end-to-end run).

## Tests (`factory/tests/test_telemetry.py`, standard library)
- Tags: a task worktree path, a subdirectory of one, an `agent-` worktree, a plain directory; role from `agent_type`, from a slug, neither; model from the payload, from a transcript tail, from the subagent transcript, missing.
- `hook`: one case per row of the table asserting the exact line; unknown event recorded with tags only; invalid JSON on stdin, and an unwritable store (a file where the directory should be), both exit 0 with empty stdout and stderr, run as a real subprocess of `bin/factory hook`.
- Feedback parsing: heading forms (`## Factory feedback`, `**Factory feedback**`, `Factory feedback:`), bullets, numbers, multi-line items, no bullets, `none`, `None.`, no heading, a following `#` heading ends the section, the last heading wins.
- Filing from `SubagentHandback`, from a `DONE:` `SendMessage` (and not from a `QUESTION:`), not from `SubagentStop`; the same item twice folds to one entry.
- `feedback set`: status and `pr` fold, re-filing does not reopen, unique prefix, ambiguous and unknown ids; `--status` filter; `--json`.
- `retro`: a valid object stored with tags; each invalid shape refused; refused outside a task worktree; unwritable store exits 1.
- `new` and `clean` record their event, and behave the same when the store is unwritable (existing tests untouched and passing).
- `hooks.json` parses, and every command is the `factory hook` line.
- Month files, pruning, `factory log` and concurrent writers: listed in `docs/specs/event-log-bounds.md`.

## Done
`make test` passes; one real lead run with this plugin leaves events, a retrospective and feedback entries, inspected by hand; the eight items of `fa1fc39c`'s `DONE:` are filed as the first entries, by a one-off script that gives them that session's tags (version `0.1.0`), which `feedback add` cannot.
