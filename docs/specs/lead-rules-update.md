# Lead rules: decide and record; the outbox holds key decisions; three fixes

## Outcome
1. A lead takes a plainly sensible decision itself and records the key ones in the outbox, one line of what and why. `QUESTION:` is an escalation and rare.
2. The outbox holds two kinds of entry, issues and decisions. Filing a decision is one command; the listing shows either kind or both.
3. Three rule fixes from repeated feedback: plain shell commands, `SPLIT:` while keeping a part, who runs a count.

## Facts
Counted over all 34 outbox entries (`factory feedback --json`, 2026-11-14), read in full by the lead:
- Compound shell commands refused: 10 entries, not the brief's nine (lead 5, reviewer 3, coder 1, explorer 1): `a5c262300989`, `b5db11c4679f`, `562f4ae8e3c6`, `fd6b1e66fdc5`, `174c05649a94`, `c6871af44f16`, `089ac47ca693`, `91be56523b08`, `0063f15d4aa0`, `beba573ed2e9`. An eleventh (`dcf4f8a0922d`, coder) was filed while this task ran. (`a68ae4271c0d` is a refused `git config` write: a different problem, not counted.)
- `SPLIT:` ambiguity: 1 (`79f989c4ef30`).
- Explorer counts too heavy: 2 (`afaf06b29a85`, `6b225404413d`).
- Decisions already in the outbox: 4, all from one lead, each filed as an ordinary item whose text starts with `DECISION: `, 85 to 150 characters.

What the worktree guard refuses, probed in this session (lead) and in a `factory:explorer` subagent (results below):
- The guard acts only on a command that names git and that it cannot read as plain. Refused: a shell loop around a git command. Passed here: a heredoc, a pipe, `;`, `cd x && git ...`, multi-line `python3 -c` that runs git through `subprocess`. Leads reported refusals for heredocs and grep patterns that merely mention git, so the boundary is not something a rule can state; "one plain command" is the form that always passes.
- In the explorer subagent (tools: Read, Bash; no Write or Edit; `CLAUDE_JOB_DIR` is set): passed: `git a; git b`, a pipe, multi-line `python3 -c` and heredoc `python3 -` that run git through `subprocess`. Refused: the shell loop, `wc -l $(git ls-files ...)`, and `cat > "$CLAUDE_JOB_DIR/tmp/x.py" <<EOF` whose body names git. So a role without a Write tool cannot rely on writing a script file; `python3 -c '<script>'` with git called through `subprocess` is the form that worked in both sessions. The reviewer has the same Bash-only tools (`factory/agents/reviewer.md` front matter) and reported the same working form (`174c05649a94`, `91be56523b08`); it was not probed separately.

## Design

### Outbox kinds
One rule: an entry whose text starts with `DECISION:` is a decision; every other entry is an issue. No new stored field, so the four existing decisions and every existing issue read correctly without migration, and a decision is visible as such in the plain listing.
- `factory feedback decide '<what>. Why: <why>'` files one decision (single quotes, because leads write identifiers in backticks, which double quotes would run; so no apostrophes in the text): whitespace is collapsed to single spaces, the text is stored as `DECISION: <text>`, tagged like `add` (role from the working directory: `lead` in a task worktree). Empty or longer than 200 characters after collapsing: exit 2 with one line. It prints the id. The same text filed twice is one entry (same id rule as today).
- `factory feedback --kind issue|decision` filters the listing; without it both kinds show. It combines with `--status` and `--json`.
- `--json` entries gain `kind` (`issue` or `decision`), computed on read, never stored.
- Statuses, `set`, hook filing and `add` are unchanged. An item in a report's "Factory feedback" section that starts with `DECISION:` is therefore a decision too.

### Rule texts
`factory/roles/lead.md`:
- Decisions (in "Talking to the dispatcher"): take the plainly sensible option without asking; record a key decision with `factory feedback decide`; a key decision is one the pull request's reviewer or a later re-review would want to know was made: it changes what a user sees, departs from the brief, or could reasonably have gone the other way. `QUESTION:` is an escalation, for what only the human can decide. This replaces "Ask only what you cannot settle ...; state the defaults you chose in the spec".
- `SPLIT:` for a part the lead is not keeping does not stop the part it keeps.
- Counts: the lead runs a count itself when one script answers it; the explorer is for searches across many files. The "full count" rule no longer names the explorer.
- Shell: one plain command per Bash call; anything longer goes in a script file under `$CLAUDE_JOB_DIR/tmp` first.

`factory/agents/{coder,explorer,reviewer}.md`: the shell rule, in the form each role can follow: the coder writes a script file with its Write tool; the explorer and reviewer, which have none, use `python3 -c '<script>'` and call git through `subprocess`.

`factory/skills/dispatch/SKILL.md`: the outbox holds issues and decisions; `--kind`; on `DONE` the dispatcher also reports the task's decisions; its critique watches for questions a lead should have settled itself. `docs/specs/factory.md`, `docs/specs/factory-events.md` and `README.md` are brought in line.

### Repo-level copies
`.claude/agents/*.md` and `CLAUDE.md` serve sessions that work in this repo without the plugin. They get the shell rule (agents) and the count rule (`CLAUDE.md`), because the same guard and the same explorer cost apply there. They do not get decisions or `SPLIT`: there is no dispatcher or outbox outside the factory.

## Defaults chosen
- Kind is derived from the text prefix, not stored: the smallest design that classifies the four existing decisions correctly.
- 200-character limit on a decision: the human asked for "very succinct"; the longest existing one is 150.
- `decide` is a `feedback` action, not a top-level command: the neighbouring task `explicit-task-state` adds top-level commands.

## Tests
`factory/tests/test_telemetry.py`: `decide` stores the prefixed one-line text with role `lead`; empty and over-long text exit 2 and file nothing; `--kind` filters both ways and combines with `--status`; `--json` carries `kind`; an old-style `DECISION:` item filed through `add` reads as a decision.

## Out of scope
The dashboard; other outbox entries; marking entries accepted or done.
