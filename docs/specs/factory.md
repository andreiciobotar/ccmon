# Spec: the factory

One dispatcher session talks to the human and hands each task to a lead session running in its own git worktree. Leads plan, build through subagents, and land a pull request. Packaged as a Claude Code plugin so any repo can use it.

## Facts this depends on
Verified on Claude Code 2.1.325 by running them in this repo, unless marked otherwise.
- `claude --bg --worktree <name> -n <name> --model <m> --permission-mode <mode> --append-system-prompt <text> "<prompt>"` starts a background session, prints `backgrounded · <id> · <name>`, and returns. The worktree is `<repo>/.claude/worktrees/<name>` on branch `worktree-<name>`, branched from the remote default branch.
- `claude agents --json [--cwd <path>] [--all]` lists sessions as objects with `id`, `sessionId`, `name`, `cwd`, `kind` (`background` or `interactive`), `status` (`busy`, `idle`), `state` (`working`, `done`, ...).
- `claude rm <id>` removes a background session's job state, its worktree and its branch. `claude stop <id>` stops it. `claude attach <id|name>` opens it interactively.
- Sessions message each other with the `SendMessage` tool by session name, in both directions, and a message wakes an idle background session. A session can subscribe to one notice when another goes idle. Tested between an interactive session and a `--bg` session, both in a prompting permission mode. No file-based inbox is needed.
- A lead's worktree is branched from the remote default branch, so a lead sees only what has been pushed there, unless the task is stacked on another task's branch (below).
- `--max-budget-usd` works only with `--print`; nothing in the CLI caps what a background session spends.
- Documented, not yet run here: `claude --worktree <name>` with the name of an existing worktree opens that worktree instead of creating one (stacked tasks rely on this); subagents may nest three levels below the main session; `--plugin-dir <dir>` loads a plugin for one session and carries over to `--bg`; plugin agents are addressed as `<plugin>:<name>`; plugin `bin/` is on the Bash tool's PATH; `.worktreeinclude` at the repo root copies matching git-ignored files into new worktrees; a blocked permission prompt in a background session shows as needing input in `claude agents`.
- Every session's shell has `CLAUDE_CODE_SESSION_ID`; `claude agents --json` lists interactive sessions with `sessionId`, `name`, `cwd`, `status` (`busy`, `idle`, `waiting`) and no `id` or `state`. A background session that has finished a turn shows `status: idle`, `state: done` whether or not its task is finished, so session state cannot tell a waiting lead from a finished one.
- Settled by the first live trial (task `ci-workflow`, 2026-11-14): a lead in `auto` permission mode pushed its branch and opened a draft pull request unattended; `claude --worktree <name>` reuses an existing worktree of that name, so stacked tasks work; the lead reached the dispatcher by session name with `SendMessage`, which the lead must first load through `ToolSearch`; `factory:explorer` and `factory:reviewer` resolved by those names; a plugin agent's bare `skills: [coding-taste]` entry preloads the plugin's skill (confirmed in an empty folder with only `--plugin-dir`); a lead that has finished shows `status: idle`, `state: done` (while its process is alive; see the next fact).
- Counted on 2026-11-14 over 17 sessions and 11 worktrees of this repo (`docs/specs/finished-lead-sessions.md`): a background session whose process has exited is listed only with `--all`, as `id`, `sessionId`, `name`, `kind: background`, `state: done`, `startedAt` and `cwd` set to the main checkout, with no `pid` and no `status`; every live session has a `pid` and a `status`, and a live lead's `cwd` is inside its worktree. A lead's worktree is locked (`locked claude session <slug> (pid N start <date>)` in `git worktree list --porcelain`) while its process lives. The one exited lead counted had no lock; a lock that outlived its process was reported once and not reproduced. `claude rm` works on exited sessions and refuses when removal is not safe (its help text).
- Still unverified: the `state` of a background session waiting on input (the trial lead never asked a question); `factory:coder` (the lead wrote a 29-line file itself); exactly what `claude rm` checks before deleting; a real `claude rm` of an exited lead.
- Observed on this machine: one repo holds 22 worktrees, 7 of them leftover per-agent ones named `agent-<hex>`; 50 of 70 worktree paths seen in transcripts no longer exist.

## Roles
| Role | Runs as | Model | Does |
|---|---|---|---|
| Dispatcher | the session the human talks to, with the `dispatch` skill loaded | fable (for now) | intake, naming, starting leads, relay, status, landing order, critique of the factory itself |
| Lead | one background session per task, in the task's worktree | fable | plan, spec, quality mandate, runs the loop, lands the PR |
| Reviewer | subagent of the lead | opus | spec conformance, bugs, test quality, smallest complete design |
| Coder | subagent of the lead | sonnet | implementation |
| Explorer | subagent of the lead or coder | haiku | facts with evidence |

The dispatcher never plans, writes code or reviews a task. When unsure whether a brief is one task or several, or how to approach it, it starts one lead.

## Plugin layout
```
.claude-plugin/marketplace.json     repo root: lists the plugin at ./factory
factory/.claude-plugin/plugin.json  name "factory"
factory/agents/{explorer,coder,reviewer}.md
factory/skills/coding-taste/SKILL.md
factory/skills/dispatch/SKILL.md    the dispatcher's rules; the human runs /factory:dispatch
factory/roles/lead.md               the lead's rules; appended to the lead's system prompt
factory/bin/factory                 Python 3.11+, standard library only
factory/tests/                      unittest
```
The plugin is the single source for the agents and the skill. The copies under this repo's `.claude/` stay until the plugin is installed here, then go.

## Task identity
One slug per task: lowercase letters, digits and single hyphens, 3 to 40 characters, not starting with `agent-`. Everything derives from it: worktree `.claude/worktrees/<slug>`, branch `worktree-<slug>` (Claude Code's own convention, kept), session name `<slug>`, spec `docs/specs/<slug>.md`.

## `factory` command
Run from inside a repo (the main checkout or any task worktree). Exit 0 on success, 2 on a usage or precondition error with one clear line on stderr, 1 when an external step (`claude`, `git`, `gh`) fails. `--json` on `status`.

A session belongs to a task when its `cwd` is the worktree path or inside it, or when it is the task's exited lead: a background session with no `pid`, named like the slug, whose `cwd` is the main checkout (the name alone is not unique across repos). Sessions without a `cwd` belong to none, and `clean` removes nothing (exit 2) while any such session is unfinished, since it could be using any task.

- `factory new <slug> (--brief <text> | --brief-file <path>) [--dispatcher <session-name>] [--base <slug>]`
  Validates the slug; refuses if the worktree, branch or a session of that name exists, if any session's `cwd` is inside the would-be worktree path, or if any pull request (open, closed or merged) already exists for branch `worktree-<slug>` (a reused name could match an old merged pull request); refuses if the repo is at its cap, listing the tasks that hold a slot (see "The cap" below). `--dispatcher` defaults to `dispatcher-<repo directory name>`; the calling session (found by `CLAUDE_CODE_SESSION_ID` in `claude agents --json`) must carry exactly that name, otherwise it refuses and says to rename the session, because leads reach the dispatcher by name. It also refuses when another listed session carries the same name. The repo directory name is always the main checkout's, also when run from a worktree. With `--base`, the task is stacked on an open task: the base task must exist and be on a branch (not detached), the worktree is created first with `git worktree add -b worktree-<slug> .claude/worktrees/<slug> <the base task's actual branch>`, and that branch is recorded in `git config branch.worktree-<slug>.factoryBase`. If the launch then exits non-zero and printed no `backgrounded` line, the new worktree and branch are removed again; in every other failure case (a session may have started) nothing is removed and the output is reported. Without it the base is the default branch and Claude Code creates the worktree. Then starts the lead with the `claude --bg --worktree` command above, adding `--plugin-dir <this plugin>`, the model and permission mode from settings, and `factory/roles/lead.md` as the appended system prompt. The initial prompt carries the slug, the dispatcher's session name, the base as a branch name (`main`, not `origin/main`) plus the remote it lives on, the start time (UTC), the time limit and the brief. Prints the session id and name, and declares the task `working` (see "Declared state" below).
- `factory status [--json]`
  One row per task worktree (directories under `.claude/worktrees/` not named `agent-*`): slug, phase (`motion`, `blocked`, `review`, `merged` or `settled`, see "The cap" below), the state its lead declared (`-` when none), branch, session id, base branch (the recorded `factoryBase`, else the default branch), session state (from `claude agents --json --all`, matched as above; `none` if no session), time since the session started and `overtime` when a session that is still working has passed the time limit, uncommitted changes, commits ahead of and behind its base, time of last commit, pull request state and URL (`gh`, when available; otherwise `unknown`), and `stale` when the last commit and the session's start are both older than 7 days (the CLI exposes no last-activity time). Then one line: the number holding a slot against the cap and how many of those are blocked, the number waiting for review, the numbers merged and settled and not yet cleaned (`--json`: `in_motion`, `blocked`, `in_review`, `merged`, `settled`; `open` is the number of task worktrees; each row carries `state` and `deliverable`, `null` when undeclared), and separately the number of leftover `agent-*` worktrees and of worktrees registered outside `.claude/worktrees/`. Then one line per task in `review`: `waiting on the human: <slug> <deliverable, else pull request URL, else ->`.
- `factory clean [--dry-run]`
  Removes work, so it acts only on positive evidence and keeps a task whenever anything is uncertain. If sessions cannot be listed, it removes nothing and exits 2. A task is removed only when all hold: it is finished by one of two rules; no uncommitted changes; no task is stacked on it (a task whose own pull request is merged into the default branch no longer counts as stacked, whatever its recorded base says, so a merged stack goes in one run); no interactive session belongs to it; every background session belonging to it is finished, meaning `state: done` with either `status: idle` or neither `status` nor `pid` (the process has exited); any other, missing or unknown value means keep, and so do two or more exited sessions on one task, since which one owns the worktree cannot be told; no other registered worktree lies inside its directory. Sessions are listed again immediately before each removal and the task's sessions rechecked. Rule 1: its pull request is merged into the default branch (a pull request merged into another task's branch does not count; no pull request, or an unknown state, does not count) and the local head is non-empty and equals the merged head. Rule 2: its declared state is `acknowledged` or `dismissed`, its pull request state is known and not open, a deliverable is recorded that is a link or a file not inside the worktree, and its head commit is on some remote-tracking branch (`git branch -r --contains HEAD`; an error means keep), so settling a task never discards unpushed commits; a task dropped with unpushed commits is removed by hand. A branch that merely sits on its base with no pull request and no settled hand-over is a fresh task, not a finished one. Removal is `claude rm <id>` when a background session exists, else `git worktree remove` plus `git branch -d`. A task that passes every check but has a locked worktree and no session is kept, with the lock reason and the command to run (`git worktree unlock <path>`, once nothing uses it), because `git worktree remove` refuses a locked worktree. A failed step is reported with the branch name. Never forces, so `git branch -d` may refuse a dismissed branch that is not merged; that is reported as a failure. Git-ignored files in a removed worktree (build output, local env files) are discarded with it; "no uncommitted changes" does not cover them. Prints what it removed and, for each task it kept, the reason.
- Settings: `~/.claude/factory/settings.json`, all optional: `worktree_cap` (default 5, tasks in motion per repo), `lead_model` (default `fable`), `lead_permission_mode` (default `auto`), `task_time_limit_hours` (default 2). A missing file means defaults; a file that is not valid JSON, or an invalid value, is a precondition error naming the file or key. The dashboard will edit this file later.

- `factory state working | blocked | handed-over <deliverable>`
  Run by a lead inside its task worktree (the task whose worktree contains the current directory; anywhere else is exit 2). Appends the declaration to the store. `<deliverable>` is required with `handed-over` and refused otherwise. One matching `^[a-z][a-z0-9+.-]*://` is a link, stored as given; anything else is a file, resolved to a real absolute path and refused when it does not exist or is inside the worktree, which `clean` removes. Prints `<slug>: <state>`.
- `factory ack <slug> acknowledged | dismissed`
  Run by the dispatcher on the human's word. The slug must be a task worktree whose declared state is `handed-over`, `acknowledged` or `dismissed`; otherwise exit 2. The record copies the deliverable. A lead that resumes afterwards declares `working`, which voids it.

### Declared state
A lead says where its task stands; the pull request and the session are no longer the only signs. Declarations are appended to `~/.claude/factory/tasks.jsonl` (a lead in its worktree can write there; it cannot write shared git config), one JSON line each: the tags of the other stores (`repo` is the main checkout's real path, `slug`, `role`, `session`, `ts`, `version`) plus `state` and, from the hand-over on, `deliverable`. A task's declared state is the `state` of the last record with its repo and slug; no record, an unknown state or an unreadable store means undeclared, and the task is judged as before declarations existed.

| Declared state | Written by | Meaning |
|---|---|---|
| `working` | `factory new` at start; the lead whenever it resumes | being built |
| `blocked` | the lead before `QUESTION:`, `BLOCKED:`, `OVERTIME:` (not `SPLIT:`: the lead carries on with the part it keeps) | waiting on the human |
| `handed-over` | the lead before `DONE:`, and again before it reports after any later turn (comments addressed, a rebase pushed) | the deliverable (a pull request, a link or a file outside the worktree) is with the human |
| `acknowledged`, `dismissed` | the dispatcher, on the human's word | the human accepted, or dropped, handed-over work |

`factory new` resets a reused slug to `working`. Known limit: a worktree made by hand under a slug used before inherits that slug's last state.

### The cap
`worktree_cap` limits how much is being built at once, not how much is waiting on the human. `new` and `status` share one definition. "A session is unfinished in it" is the negation of `clean`'s test (no interactive session belongs to the task, and every background session that belongs to it is finished, as defined there), or the sessions cannot be told: they cannot be listed, or an unfinished one has no `cwd` and could be in any task.

| Phase | When | Holds a slot |
|---|---|---|
| `motion` | a session is unfinished in it; or declared `working`; or undeclared and its pull request is not open or merged | yes |
| `blocked` | declared `blocked`, no session unfinished | yes |
| `merged` | no session unfinished, pull request merged, and handed over (declared `handed-over`, `acknowledged` or `dismissed`) or undeclared | no |
| `settled` | no session unfinished, declared `acknowledged` or `dismissed`, pull request not merged | no |
| `review` | no session unfinished, and declared `handed-over` with its pull request not merged, or undeclared with its pull request open | no |

So a declared `blocked` or hand-over is read only once the lead's turn has ended, and a lead that resumes counts as `motion` while it runs even if it forgot to declare `working`; a declared `working` or `blocked` wins over an open pull request, so a lead with an early draft that stops on a question is `blocked`, not `review`; a declared hand-over needs no pull request and stays `review`, listed by `status`, whatever the pull request state (none, closed, unknown) until it is merged or the human settles it with `factory ack`. An undeclared task (started before declarations existed) keeps the old inference: a finished lead with no pull request, a closed pull request, an unknown pull request state or a detached HEAD count as `motion`, because a slot opens only on positive evidence. Only `factory new` checks the cap, so a lead that resumes is never stopped by it and the number in motion can exceed it. Nothing bounds the number waiting for review; `status` shows it.

One function builds the lead's launch command. That is the only place a different runner (another provider's CLI) would plug in; no runner abstraction is built now.

## Relay
Native session messaging only.
- Lead to dispatcher: before each message the lead declares its state with `factory state` (`blocked`, or `handed-over <deliverable>` before `DONE:`; `working` when it resumes), then `SendMessage` to the dispatcher's session name. The first line starts with one of `QUESTION:`, `BLOCKED:`, `OVERTIME:`, `SPLIT:`, `DONE:`, followed by the slug. A lead that asks a question ends its turn and waits. A `QUESTION:` is an escalation and rare: a lead takes a plainly sensible decision itself and records the key ones (those a reviewer of the pull request or a later re-review would want to know were made) in the outbox with `factory feedback decide`, one line of what and why.
- Dispatcher to human: every `QUESTION`, `BLOCKED`, `OVERTIME` and `SPLIT` is shown in the lead's own words, with the slug, never summarised away or answered on the human's behalf.
- Human to lead: the dispatcher sends the human's answer or question to the lead by slug, in the human's words plus any context the lead lacks.
- The human can always `claude attach <slug>` and talk to a lead directly.
- The dispatcher holds no state of its own: everything it knows is recoverable from `factory status` and the leads. It uses a stable session name per repo (`dispatcher-<repo>`); only the human can name a session (`claude -n <name>` or `/rename <name>`), so the dispatcher asks for that on start and `factory new` enforces it. On start it sweeps: any lead that is idle in `motion` is asked what it needs. Whenever it reports it repeats every `waiting on the human` line until that work is merged, acknowledged or dismissed, and it records the human's word with `factory ack`. A lead whose message cannot be delivered ends its turn with that message as its final output and waits.

## Time limit
A task that has run longer than the limit (2 hours by default, counted from the session's start) is paused and surfaced.
- The lead owns the call: at every checkpoint (each time a subagent returns, and before starting one) it checks the clock. Past the limit, or earlier when it judges the task is not converging (the same issue back from review a third time, a plan that keeps growing), it starts no new work and sends `OVERTIME:` with diagnostics: what is done, what is left, why it is slow, what it has cost so far if it can tell, and the options (continue with a new deadline, cut scope, split, abandon) with its recommendation. Then it waits.
- The dispatcher is the backstop: `factory status` flags `overtime`; a flagged lead that has not reported is told to pause and report, and one that does not respond is stopped with `claude stop` (resumable) and surfaced.
- Known limit: the lead can only check between subagent runs, so a pause can lag by the length of one run.

## Stacked tasks
A task may build on another open task instead of waiting for its pull request to merge.
- The stacked task's worktree branches from the base task's branch, its lead reconciles with that branch (not the default branch) when landing, and its pull request targets that branch.
- When a base task changes after review, the dispatcher tells the leads stacked on it to rebase onto the new tip and rerun their tests; when a base merges, they rebase onto the default branch, retarget their pull requests and clear the recorded base (`git config --unset branch.worktree-<slug>.factoryBase`). A lead that has already finished is never told, so `clean` does not rely on this: once a task's pull request is merged into the default branch its recorded base no longer protects the base task.
- Rebasing a pushed branch needs a forced push. A lead may run `git push --force-with-lease` on its own task branch and nothing else: never a plain force, never another branch.
- The dispatcher keeps stacks shallow (three deep at most, as a rule of thumb) and lands them bottom-up.

## Lead lifecycle
1. **Plan:** read the brief and the repo; write `docs/specs/<slug>.md` including the facts it depends on; get a full count for any fact that rests on a sample (the lead runs it when one script answers it; the explorer is for searches across many files); choose the approach a strong practitioner would choose today, checked against current documentation and prior art rather than assumed.
2. **Build:** coder against the spec file, one writer in the worktree at a time. Work that splits cleanly along an interface written in the spec may use parallel coders in child worktrees (`isolation: worktree`), merged back by the lead.
3. **Review:** full review by the reviewer after each implementation; a commit per coder round; blocker fixes re-reviewed by a fresh reviewer on the commit range; minor-only rounds checked by the lead.
4. **Quality assessment** (the lead's own, beyond the reviewer): state of the art, blind spots, regressions, undocumented behavior, simplicity, operability. Each item states what was verified, how, and what remains unverified.
5. **Land:** fetch; rebase onto the default branch; run the full tests; final review scaled to what the default branch did (unchanged: none; changed without overlap and tests pass: light; conflicts or overlap: full review of the affected area); push; open a draft pull request against the task's base; declare `factory state handed-over <URL>`; send `DONE:` with the URL. The body is written for a decision in one or two minutes: outcome title, what changes (one to three bullets), where to look (one to three `path:line` spots and why), verified (one line), not verified or risks (one line), about 120 words in all; the spec link, the full quality assessment and test output sit below in a collapsed section. If pushing or opening the pull request is denied, the lead writes the pull request body to `docs/specs/<slug>.pr.md`, commits it and sends `BLOCKED:`; the dispatcher shows the human and, on their word, pushes and opens the pull request from the attended session, then sends the lead the URL so that it declares the hand-over.
A task that turns out to be several is reported with `SPLIT:` and one brief per task the lead is not keeping; the dispatcher starts them. The lead carries on with the part it keeps.

Every role runs one plain command per Bash call, because worktree isolation refuses compound commands that mention git; a longer step is a script file under the job's temporary directory (lead, coder) or `python3 -c` (explorer and reviewer, which have no Write tool).

## Dispatcher's critique duty
The dispatcher watches how the factory performs (lead reports, `factory status`, and `ccmon --json` when installed) and tells the human what to change: where review loops pile up, which briefs split badly, which rules leads trip over, what the tokens bought. It proposes edits to the plugin's files; the human decides.

## Tests
- Slug validation, both sides of every rule.
- `new`: cap refusal on both sides of every case in "The cap" (only the tasks in motion are listed; below the cap no other task's pull request is looked up), name collisions, the dispatcher-name check (wrong name, session not found), `--base` with a base task on a branch not named `worktree-*`, the two launch-failure cases, `--base` (unknown base refused, the worktree and config commands issued, base recorded), the exact launch command for given settings (the `claude` and `git` calls go through one injectable runner so tests assert the argv without starting sessions).
- Malformed `gh` output (entries that are not objects, a non-string `state`) yields pull request state `unknown`, never a traceback.
- `status`: rows built from canned `git worktree list --porcelain`, `claude agents --json` and `gh` outputs, including `agent-*` and outside worktrees, a missing `gh`, the recorded base, the overtime flag on both sides of the limit, the stale rule, and the phase of each row with the counts.
- Declared state: see `docs/specs/explicit-task-state.md` (store, `state`, `ack`, every phase row, the cap, `status` output, `clean` rule 2 on both sides of each check).
- `clean`: sessions unlistable (missing `claude`, bad JSON, an entry that is not an object) removes nothing; an unfinished session with no `cwd` removes nothing; every keep reason on both sides (fresh task with no pull request, pull request merged into another task's branch, empty head, uncommitted changes, stacked-on (kept while the stacked task is open, has no pull request or was merged into another task's branch; removed once it is merged into the default branch with its recorded base never cleared), another worktree nested inside, interactive session in the worktree or a subdirectory, background session busy, waiting, `idle` with `state: working`, unknown status, missing status with a `pid`, an exited session that is not `done`, two exited sessions, a locked worktree with no session, a session that becomes active between listing and removal, detached head, unreadable git status); the two removal paths (an exited lead, locked or not, goes through `claude rm`), one real removal end to end in the temporary repo, `--dry-run` changes nothing.
- Settings: defaults, overrides, invalid values.
- One end-to-end test in a temporary git repo with a real `git` and a fake `claude` on PATH.

## Out of scope here
Dashboard changes (task grouping, needs-you state, the cap setting), other providers, removing this repo's `.claude/agents` copies.
