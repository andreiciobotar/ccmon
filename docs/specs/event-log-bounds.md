# Spec: the factory's log files stay bounded

The rule itself is the "Store" section of `docs/specs/factory-events.md` (already rewritten on this branch; read it first). This file holds the facts, the decisions, the code shape and the tests.

## Facts this rests on
1. Full count over the live store on 2026-11-14 (`~/.claude/factory/`, written by up to five concurrent leads and their subagents): `events.jsonl` 107 lines, `feedback.jsonl` 52, `retros.jsonl` 6, `tasks.jsonl` 1. Malformed lines (not a JSON object, or a missing final newline): **0** in every file. Longest line 1664 bytes. So single `O_APPEND` writes have not torn or interleaved; the append path stays as it is.
2. Rate: 30 KB of events and 27 KB of feedback in the first ten hours, about 280 bytes per event. A month of heavy use is a few MB per file.
3. `read_feedback` is the only reader in the repository; it reads the whole file. Nothing reads `events.jsonl` or `retros.jsonl` yet (grep over `factory/`, `isshoni/`, `internal/`, `cmd/`).
4. Every line in the three files carries `ts` (feedback status lines too), UTC, fixed width, so string comparison orders them.
5. The branch `worktree-explicit-task-state` calls `append("tasks", ...)` and reads `tasks.jsonl`. So `append(name, record)` must keep its behavior for other names.
6. Leads run whatever plugin copy they were started with, so old copies keep appending to the unsuffixed files for as long as their sessions live.

## Decisions
- **Month files, no rewriting.** A file is only ever appended to (the current month) or unlinked whole (an old month). No rotation by rename, no compaction, so no lock: the one concurrency mechanism stays the atomic `O_APPEND` write. Size-based rotation and compaction both need a lock taken by every hook.
- **Events:** dropped after 12 full months, not archived. They are raw material; the measures compare plugin versions, and a year-old version is not a comparison anyone acts on.
- **Retrospectives:** never dropped. One line per task (about 1.2 KB), and they are the record with no "closed" state.
- **Feedback:** never dropped while open or accepted; closed (`done`, `rejected`) entries go with their file after 12 months. Before an old file is deleted, the lines of its entries that are still live are appended to the current month's file (still append-only, no lock). Without the carry, one entry left open would keep every later file forever (review finding), and decisions (pull request #8) are filed open.
- **The latest `ts` wins** among an entry's status lines, not the last line read: carried lines and old plugin copies writing the unsuffixed file both put lines out of file order.
- **12 months is a constant** (`KEEP_MONTHS`), not a setting: `load_settings` fails on a broken settings file, and a hook must not lose an event to that.
- **No migration of the unsuffixed files.** Readers read them first; they age out by the same rule using their modification time. Moving their lines would race with old plugin copies still appending (fact 6).
- **Pruning runs at the first write of a month** for that name, inside the writer. No new command, no cron, no edit to `factory clean`.
- **One reader command**, `factory log events|retros`, so the site manager and stats page need not know the layout. Not for feedback: carried lines make its month files inexact, and `factory feedback --json` already reads it.

## Code (`factory/bin/factory`, telemetry section only)
Keep edits local: `append` is untouched; `cmd_new`, `cmd_status`, `cmd_clean`, `load_settings` are untouched.

- `KEEP_MONTHS = 12`.
- `log(name, record)`: `append` to `<name>-<record["ts"][:7]>`; when that file did not exist before the write, call `prune(name)`. The five existing `append("events"|"feedback"|"retros", ...)` calls become `log(...)`.
- `log_files(name)`: `[(month, path)]`, oldest first: the unsuffixed `<name>.jsonl` first when it exists (its month is the UTC month of its mtime), then `<name>-YYYY-MM.jsonl` by name. Names that do not match exactly that pattern are ignored (`events-2026-11.jsonl.bak`, `events-old.jsonl`).
- `read_log(name, since=None, until=None)`: yields the JSON objects of those files in order. A month file is skipped when its month is outside `since[:7] .. until[:7]`; the unsuffixed file is always read. A record is yielded when `since <= ts < until` (a missing bound does not filter; a record without a string `ts` is yielded only when no bound is given). Lines that are not JSON objects are skipped. A file that vanishes between listing and reading is skipped.
- `read_feedback()` folds `read_log("feedback")`; the fold itself takes records, so `prune` can fold a subset of files.
- `prune(name)`: best effort, swallows every exception. `cutoff` = the month `KEEP_MONTHS` before the current UTC month; a file is old when its month `< cutoff` (so the current month and the 12 before it are kept). While the oldest file (listed afresh each round) is old:
  - `events`: unlink it.
  - `feedback`: fold all files (`before`) and all but the oldest (`after`), as `{id: (status, pr)}`. The ids to carry are those whose value differs between the two and that either remain in `after` or are not `done`/`rejected`. Append every line of the oldest file with such an id to the current month's file, in order, then unlink it. A failed append leaves the file.
  - `retros`: nothing.
- The feedback fold takes an entry's status from the status line with the greatest `ts`, the later line on a tie.
- `factory log {events,retros} [--since TS] [--until TS]`: prints the records of `read_log`, one JSON object per line. A bound is a `ts` prefix of at least `YYYY-MM` (regex `\d{4}-\d{2}(-\d{2}(T\d{2}(:\d{2}(:\d{2}Z?)?)?)?)?`); anything else exits 2. Empty store: prints nothing, exits 0.

`README.md` (the paragraph naming the three files and the command list) and `factory/skills/dispatch/SKILL.md` (the one sentence naming the files) are brought in line with the smallest edit: other open pull requests touch the neighbouring lines.

## Tests (`factory/tests/test_telemetry.py`, standard library)
Existing tests keep passing with the `lines()` helper reading through the month files.
- `log` writes to the file of the record's month, not the clock's; `append("tasks", ...)` still writes `tasks.jsonl`.
- `log_files`: unsuffixed first, months in order, stray names ignored.
- `read_log`: half-open bounds with prefixes of several lengths (`2026-11`, `2026-11-14`, a full `ts`); month files outside the range are not opened (make one unreadable garbage and assert on the result, or patch `open`); the unsuffixed file is always read and filtered by `ts`; malformed and non-object lines skipped; a vanished file skipped.
- Feedback across files: an entry in the unsuffixed file with its status line in a month file folds; `feedback set` on such an entry works.
- `prune("events")`: with the clock patched, a file 13 months old is removed, one 12 months old and the current one stay; an old unsuffixed file (mtime set with `os.utime`) is removed, a fresh one stays.
- `prune("feedback")`: an old file with only `done`/`rejected` entries goes; `open` and `accepted` entries of old files (unsuffixed and month) end up in the current month with the same folded status, closed ones gone; a status line in a later old file follows its carried entry; an entry closed in the old file and re-filed later stays closed; a carry that cannot be written keeps the old file.
- The fold: a status line with a later `ts` in the unsuffixed file beats an earlier one in a month file.
- `retros` are never pruned.
- The first write of a month prunes (end to end through `factory hook` as a subprocess is not needed; call `log` with the clock patched), and a failing prune (unlink raising `OSError`) does not fail the write.
- Concurrency, real processes: 8 processes each appending 300 records through `log` (line lengths from 100 bytes to 16 KB), while another process calls `prune("events")` in a loop over a store that also holds old month files. Afterwards: exactly 2400 lines in the current month, every one parses, every (process, sequence) pair present once, the old files are gone.
- `factory log`: bounds, bad bound exits 2, empty store, `feedback` refused.

## Done
`make test` passes. Run against a copy of the live store: `factory feedback --json` prints the same entries before and after the change; `factory log events` prints the 100+ existing events; a new hook event lands in `events-2026-11.jsonl`.
