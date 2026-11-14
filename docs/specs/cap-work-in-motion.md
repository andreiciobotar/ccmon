# Spec: the worktree cap counts work in motion

The human's rule: "the rule is work in motion, not open PRs. when the PR opens, the slot should open."

## Facts this depends on
Checked on this machine, Claude Code 2.1.325, 2026-11-14.
- `gh pr list --json state` gives `OPEN`, `CLOSED` or `MERGED`; a draft is `OPEN` (`isDraft` is a separate field). The lead rules open every pull request as a draft, so draft-ness does not separate "opened early" from "landed".
- A live lead is listed with `cwd` inside its worktree; while it works, `status: busy`, `state: working`; after a turn ends, `status: idle`, `state: done`, whether or not the task is finished (docs/specs/factory.md).
- A lead whose process has exited is listed with `state: done`, no `status`, no `pid`, and `cwd` at the main checkout (seen on `task-grouping` and `factory-events`), so `sessions_in` does not match it to its task: the task shows session `none`. Fixing that is a separate task (sent as `SPLIT:`); this change gives the right phase either way, because an exited lead is not working.
- `factory status --json` has no consumer in this repo other than the tests (grep for `leftover_agent`, `"open"`).
- `git branch -d` removes the branch's `branch.<name>.*` config, so a removed task leaves no recorded base behind.

## Definition
Each task worktree is in one of three phases, computed by one function used by both `new` and `status`:

| Phase | When | Counts against the cap |
|---|---|---|
| `review` | its pull request is open and no session is unfinished in it | no |
| `merged` | its pull request is merged and no session is unfinished in it | no |
| `motion` | everything else | yes |

"No session is unfinished in it" is the test `clean` already uses (`session_block`): no interactive session belongs to it, and every background session that belongs to it is exactly `status: idle`, `state: done`. A task with no session passes.

The cases:
- **Draft opened early, lead still building:** `motion` (its session is working).
- **Open pull request, lead working again** (review comments, rebase): `motion` while it works, `review` again when its turn ends. The cap is checked only by `factory new`; a lead that resumes is never stopped by it, so the number in motion can exceed the cap.
- **Finished lead, no pull request:** `motion`. Nothing has been handed over; it needs the dispatcher or the human.
- **Pull request closed without merging:** `motion`. The work is neither under review nor done; reopen it or remove the task.
- **Pull request state unknown** (`gh` missing or failing), **detached HEAD**, or **sessions cannot be listed**: `motion`. A slot opens only on positive evidence.
- **Merged, not yet cleaned:** `merged`, does not count; `factory clean` removes it.

Known limit: a lead that opened a draft early and then ended its turn (a `QUESTION:`, a `BLOCKED:`) reads as `review`, since session state cannot tell a waiting lead from a finished one. It is waiting on the human in both cases.

Nothing bounds the number of tasks waiting for review; `status` shows it. The review queue is the human's own, and a second cap would block new work on review again, which is what this change removes.

## Changes
`factory/bin/factory`:
- `phase(pr_state, mine)` returns `motion`, `review` or `merged`; `mine` is the task's sessions, or `None` when sessions cannot be listed.
- `collect` adds `phase` to each row.
- `cmd_new`: when the repo has at least `worktree_cap` task worktrees, it asks `gh` for each task's pull request and refuses only if the tasks in `motion` number at least the cap: `at the cap of N tasks in motion: a, b, ...`. Below the cap it makes no extra calls.
- `cmd_status`: a `PHASE` column after `SLUG`; the count line becomes `in motion M/CAP; waiting for review R; merged, not cleaned G; leftover agent worktrees L; worktrees outside .claude/worktrees O`. `--json` gains `in_motion`, `in_review`, `merged` and each row's `phase`; `open` stays the number of task worktrees.
- `keep_reason` (`clean`): a task whose own pull request is merged into the default branch no longer counts as stacked on its recorded base, so a merged stack is removed in one run even when nobody cleared `factoryBase`. An open or unmerged stacked task still protects its base.

`worktree_cap` keeps its name: settings files and the dashboard brief refer to it.

Docs: `docs/specs/factory.md` (`new`, `status`, `clean`, settings, stacked tasks, tests), `factory/skills/dispatch/SKILL.md`, `factory/roles/lead.md`, `README.md`.

## Tests
- `phase`: every case in the list above, both sides.
- `new` at the cap: refused when all are in motion (unknown pull request state included); allowed when one task has an open pull request and no session, or a finished session; refused when that task's session is working, when its pull request is closed, when it has none; a merged task frees a slot; below the cap no `gh` call is made for other tasks.
- `status`: `phase` per row, the three counts in JSON, the count line, the `PHASE` column.
- `clean`: a base whose stacked task is merged into the default branch is removed; a base whose stacked task is open, or merged into another branch, is kept.

## Out of scope
The dashboard, the time limit, matching exited lead sessions to their task (the `SPLIT:`), bounding the review queue.
