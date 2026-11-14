# Spec: a lead declares its task's state; hand-over frees the slot

PR #7 infers a task's phase from the lead's session state and whether a pull request exists. Two gaps: a lead stopped on a question reads as waiting for review, and a task whose deliverable is not a pull request (a video, a report) never frees its slot. Now the lead says where its task stands, and the human's acknowledgement stands in for a merge when there is no pull request.

## Facts this depends on
Checked in this worktree, 2026-11-14.
- A lead in its worktree can write to `~/.claude/factory/`: `echo x >> ~/.claude/factory/<file>` succeeded from this lead session; `events.jsonl`, `feedback.jsonl` and `retros.jsonl` there are written from worktrees today. (Shared git config is not writable from a worktree session, so it is not the store.)
- `factory status --json` has no consumer outside `factory/tests` (grep for `in_motion`, `in_review`, `factory status` over `cmd/`, `internal/`, `ccmon/`, `README.md`, the plugin).
- `git worktree list --porcelain` run inside a task worktree lists the main checkout first, so `Repo()` gives the same `root` (a real path) to the lead and to the dispatcher. `tags()` does not resolve symlinks, so it is not used for the key.
- `append()` writes one line in one `O_APPEND` write; concurrent leads cannot interleave records.

## The store
`~/.claude/factory/tasks.jsonl`, append-only, written with the existing `append()`. One record per declaration: the usual tags from `record()` (`ts`, `repo` = the main checkout's real path, `slug`, `role`, `model` null, `version`, `session`) plus `state` and, for a hand-over and everything after it, `deliverable`.

A task's declared state is the `state` of the last record with its `repo` and `slug`. No record, or a last record whose `state` is not one of the five below, means undeclared. Unreadable lines are skipped; a missing or unreadable file means nothing is declared.

| Declared state | Written by | Meaning |
|---|---|---|
| `working` | `factory new` at start; the lead whenever it resumes | being built |
| `blocked` | the lead before `QUESTION:`, `BLOCKED:`, `OVERTIME:`, `SPLIT:` | waiting on the human |
| `handed-over` | the lead before `DONE:` | the deliverable is with the human |
| `acknowledged`, `dismissed` | the dispatcher, on the human's word | the human accepted, or dropped, handed-over work |

`factory new` writes `working` after the lead has started (a failed write is ignored, like `TaskStarted`), so a reused slug starts clean. Known limit: a worktree made by hand under a slug that was used before inherits that slug's last state.

## Commands
- `factory state working | blocked | handed-over <deliverable>` — run by the lead inside its task worktree. The task is the one whose worktree contains the current directory (real paths); outside any task worktree it exits 2. `<deliverable>` is required with `handed-over` and refused with the other two. A deliverable matching `^[a-z][a-z0-9+.-]*://` is a link and stored as given. Anything else is a file: `~` is expanded, it is resolved to a real absolute path, and it is refused (exit 2) when it does not exist or is the worktree or inside it, because the worktree is what `clean` removes. Prints `<slug>: <state>`.
- `factory ack <slug> acknowledged | dismissed` — run by the dispatcher when the human says so. `<slug>` must be a task worktree whose declared state is `handed-over`, `acknowledged` or `dismissed`; otherwise exit 2. The record copies the deliverable. Prints `<slug>: <state>`. A lead that resumes afterwards declares `working`, which voids it.

## Phase
One function, used by `new` and `status`: `phase(pr_state, mine, state)`.

"A session is unfinished in it" is unchanged from PR #7 (`session_block`, or the sessions cannot be told).

| Phase | When | Holds a slot |
|---|---|---|
| `motion` | a session is unfinished in it; or declared `working`; or undeclared and its pull request is not open or merged | yes |
| `blocked` | declared `blocked`, no session unfinished | yes |
| `merged` | no session unfinished, pull request merged, and declared handed over (any of the last three states) or undeclared | no |
| `settled` | no session unfinished, declared `acknowledged` or `dismissed`, pull request not merged | no |
| `review` | no session unfinished, and declared `handed-over` with its pull request not merged, or undeclared with its pull request open | no |

So:
- An undeclared task (started before this change) is judged exactly as before.
- A declared `blocked` or hand-over is read only once no session is unfinished in the task: the lead declares during the turn that sends the message, and a lead that resumes without declaring `working` still reads as `motion` while it runs.
- A declared `working` or `blocked` wins over an open pull request: a lead with an early draft that stops on a question is `blocked`, not `review` (PR #7's known limit).
- A declared hand-over does not need a pull request, and stays `review` whatever the pull request state (none, closed, unknown) until it is merged or the human settles it.

The cap counts `motion` and `blocked`. `new`'s refusal lists them, marking the blocked: `at the cap of 2 tasks in motion: a, b (blocked)`.

## `status`
- Rows gain `state` (the declared state or `null`) and `deliverable` (string or `null`). The table gains a `STATE` column after `PHASE` (`-` when undeclared).
- Count line: `in motion M/CAP (B blocked); waiting for review R; merged, not cleaned G; settled, not cleaned S; leftover ...` as before. `M` counts slots held (`motion` + `blocked`). `--json`: `in_motion` (= M), `blocked`, `in_review`, `merged`, `settled`.
- After the count line, one line per task in `review`, so that the dispatcher repeats it on every report until it is merged, acknowledged or dismissed: `waiting on the human: <slug> <deliverable, else pull request URL, else ->`. Not in `--json` (the rows carry it).

## `clean`
Still an allow-list. The checks that apply to every task are unchanged and come first (branch, clean tree, no unfinished session, no nested worktree, nothing stacked on it). Then a task is removed when either:
1. its pull request is merged into the default branch and the local head equals the merged head (as today, whatever is declared); or
2. its declared state is `acknowledged` or `dismissed`, and all hold: its pull request is not open (close or merge it first); a deliverable is recorded and is a link, or a file path that is not the worktree or inside it; and its head commit is on some remote-tracking branch (`git branch -r --contains HEAD` in the worktree prints at least one line; an error means keep). The last check is added to the brief's rule: an acknowledged video must not take unpushed commits with it. A task dropped with unpushed commits is removed by hand (`claude rm`).

When rule 2 does not hold for a settled task, the kept reason names the failed check; otherwise the reason is rule 1's, as today. Removal steps are unchanged (`claude rm <id>`, else `git worktree remove` + `git branch -d`; never forced, so `git branch -d` may refuse a dismissed branch that is not merged, which is reported as a failure).

## Role rules
- `factory/roles/lead.md`: a short section "Declaring your state" (which command before which message; where a file deliverable goes: the place the brief names, else `~/.claude/factory/deliverables/<slug>/`, never the worktree), and the slot paragraph rewritten. Edits stay in those two places; `lead-rules-update` is editing other sections.
- `factory/skills/dispatch/SKILL.md`: phases and the `STATE` column; on every report repeat the `waiting on the human` lines; `factory ack` on the human's word only; a `blocked` task is waiting on the human's answer.
- `docs/specs/factory.md`, `README.md`: match.

## Tests
- `phase`: every row of the table on both sides, and every undeclared case from PR #7 unchanged.
- Store: last record wins; another repo's or slug's records ignored; garbage lines skipped; unknown state means undeclared; missing file.
- `state`: each state written with the repo root and slug; outside a task worktree refused; from a subdirectory accepted; `handed-over` without a deliverable and `working` with one refused; link stored as given; file resolved to an absolute path; missing file and a file inside the worktree refused.
- `ack`: refused for an unknown slug, an undeclared task, `working`, `blocked`; accepted after `handed-over`, copying the deliverable; `working` after it voids it.
- `new`: writes `working`; at the cap, a `blocked` task counts and is marked, a handed-over task with no pull request does not count, a handed-over task with a working session counts.
- `status`: `state`, `deliverable`, phases and the five counts in JSON; the `STATE` column, the count line and the `waiting on the human` lines in the table output.
- `clean`: rule 2 on both sides of each check (state merely `handed-over`, pull request open, deliverable inside the worktree, head on no remote, `git branch -r` failing, dirty tree, unfinished session); rule 1 unchanged for declared and undeclared tasks.
- End to end in the temporary repo with real `git`: `state` from inside a real worktree, then `status --json` shows it.

## Live check
In this repo, starting no lead: from this worktree run the branch's `factory state working`, `factory status`, `factory state handed-over <PR URL>`; confirm the record in `~/.claude/factory/tasks.jsonl` and the row. `factory ack` on this task is the human's to give, so it is exercised only in tests.

## Out of scope
The dashboard, the two-hour timer, dismissing a task that was never handed over (an abandoned task still needs `claude rm`), matching exited lead sessions to their task (`finished-lead-sessions`).
