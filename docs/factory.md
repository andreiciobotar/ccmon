# factory reference

The `factory` command of the plugin (Python 3.11+, standard library; needs `git`, `claude` and, for pull request state, `gh`). Run it inside a repository: the main checkout or any task worktree. Exit codes: 0 ok, 2 usage or precondition error (one line on stderr), 1 a `claude`, `git`, `gh` or `launchctl` step failed or a log could not be written.

## Tasks
A task is one slug: lowercase letters, digits and single hyphens, 3 to 40 characters, not starting with `agent-`. From it follow the worktree `.claude/worktrees/<slug>`, the branch `worktree-<slug>`, the lead's session name `<slug>`, and the spec `~/.claude/factory/specs/<repository directory name>/<slug>.md`. Specs are working papers and are never committed; they outlive the worktree.

A session belongs to a task when its `cwd` is in the task's worktree, or when it is the task's exited lead (a background session with no `pid`, named like the slug, with `cwd` at the main checkout).

## Commands
```
factory new <slug> (--brief <text> | --brief-file <path>) [--dispatcher <session-name>] [--base <slug>]
factory status [--json]
factory clean [--dry-run]
factory state working | blocked | handed-over <deliverable>
factory ack <slug> acknowledged | dismissed
factory feedback [--json] [--status open|accepted|rejected|done] [--kind issue|decision]
factory feedback add --role <role> [--slug <slug>] [--model <model>]     # a report or items on stdin
factory feedback decide '<what>. Why: <why>'                             # 200 characters at most
factory feedback set <id> open|accepted|rejected|done [--pr <url>]       # id or a unique prefix
factory retro                                                            # a JSON object on stdin
factory log events|retros [--since <ts>] [--until <ts>]
factory hook                                                             # run by the plugin's hooks
factory health [--json] [--dispatcher <session-name>]
factory watch [--dispatcher <session-name>]
factory schedule install|remove|status [--dispatcher <session-name>]
factory firing <id> acted|ignored                                        # id or a unique prefix
factory miss <text> [--slug <slug>] [--firing <id>]
factory firings [--json]
```

- **`new`** starts a lead: a background session in a new worktree, with `factory/roles/lead.md` appended to its system prompt, declared `working`. The calling session must be named `--dispatcher` (default `dispatcher-<main checkout's directory name>`; set it with `/rename`), and no other session may carry that name. It refuses a slug whose worktree, branch, session or pull request (in any state) already exists (when `gh` cannot say, it warns and goes on), and a repository at its cap. `--base` stacks the task on another open task's branch.
- **`status`** prints one row per task worktree: slug, phase, declared state, branch, session, base, session state, age (`overtime` past the time limit), uncommitted changes, commits ahead and behind its base, last commit, pull request, and `stale` when the last commit and the session's start are both older than 7 days. Then the counts per phase against the cap, the leftover `agent-*` and outside worktrees, and one `waiting on the human: <slug> <deliverable>` line per task in review. Pull request state is `unknown` without `gh`.
- **`clean`** removes finished tasks (session, worktree, branch) and says why it kept the rest. It never forces and keeps a task whenever anything is uncertain. A task goes only when its pull request is merged into the default branch with the local head equal to the merged head, or its state is `acknowledged` or `dismissed` with a recorded deliverable, a pull request that is not open and a head that is pushed; and in both cases nothing is uncommitted, no task is stacked on it, no session in it is unfinished and no other worktree is nested in it. It keeps a task on a detached HEAD, and one whose worktree is locked with no session (it prints the `git worktree unlock` to run). If sessions cannot be listed, or an unfinished session has no `cwd`, it removes nothing (exit 2). A dismissed branch that is not merged makes `git branch -d` refuse: reported, exit 1. Git-ignored files in a removed worktree go with it.
- **`state`** is run by a lead inside its worktree. `handed-over` takes the deliverable: a link (`scheme://...`), or a file that exists and is outside the worktree.
- **`ack`** is run by the dispatcher on the human's word, for a task that was handed over.

## Declared state, phases and the cap
| Declared state | By | Meaning |
|---|---|---|
| `working` | `new`; the lead when it resumes | being built |
| `blocked` | the lead, before `QUESTION:`, `BLOCKED:`, `OVERTIME:` | waiting on the human |
| `handed-over` | the lead, before `DONE:` and after any later turn | the deliverable is with the human |
| `acknowledged`, `dismissed` | the dispatcher (`ack`) | the human accepted or dropped it |

A session is unfinished if it is interactive, or a background session, unless that one is `done` and either idle or exited.

| Phase | When | Holds a slot |
|---|---|---|
| `motion` | a session is unfinished in it, or sessions cannot be listed or placed; or declared `working`; or undeclared with no open or merged pull request | yes |
| `blocked` | declared `blocked`, no session unfinished | yes |
| `review` | no session unfinished; declared `handed-over` and not merged, or undeclared with an open pull request | no |
| `merged` | no session unfinished, pull request merged | no |
| `settled` | no session unfinished, `acknowledged` or `dismissed`, not merged | no |

The cap (`worktree_cap`) counts slots per repository. Only `new` checks it, so a lead that resumes is never stopped. Nothing bounds the number in review.

## Settings
`~/.claude/factory/settings.json`, every key optional. A file that is not valid JSON, or an invalid value, is an error naming the file or key.

| Key | Default | |
|---|---|---|
| `worktree_cap` | 5 | tasks holding a slot, per repository; `isshoni-tui` can edit it |
| `lead_model` | `fable` | |
| `lead_permission_mode` | `auto` | `acceptEdits`, `auto`, `bypassPermissions`, `manual`, `dontAsk`, `plan` |
| `task_time_limit_hours` | 2 | after it `status` shows `overtime`; the lead stops and reports |

## Messages
Leads and the dispatcher talk through Claude Code's `SendMessage`, by session name. A lead's first line starts with `QUESTION:`, `BLOCKED:`, `OVERTIME:`, `SPLIT:` or `DONE:` and its slug. The human can always `claude attach <slug>`.

## Stacked tasks
`new --base <slug>` branches the worktree from the base task's branch and records it in `git config branch.worktree-<slug>.factoryBase`; the pull request targets that branch. The record is never cleared: `status` stops showing a base once it has merged into the default branch, and `clean` stops treating a merged task as stacked. When a base merges, the stacked lead rebases onto the default branch and retargets its pull request.

## Logs
All under `~/.claude/factory/`, JSON lines, append-only, mode 0600. Except where noted, a record carries `ts` (UTC, `2026-11-14T00:01:02Z`), `repo` (the main checkout's path), `slug`, `role` (`lead`, `dispatcher`, a subagent's type without the `factory:` prefix such as `coder`, `subagent` when it has none, else `other`; `feedback add` stores the `--role` it is given), `model`, `version` (the plugin's), `session`.

| Log | Files | Written by | Kept |
|---|---|---|---|
| events | `events-YYYY-MM.jsonl` | the plugin's hooks; `new`, `clean` | the current month and the 12 before it |
| retros | `retros-YYYY-MM.jsonl` | `factory retro` | forever |
| feedback | `feedback-YYYY-MM.jsonl` | hooks; `feedback add`, `decide`, `set` | `open` and `accepted` forever, `done` and `rejected` at least 12 months |
| task state | `tasks.jsonl` | `new`, `state`, `ack` | forever |
| firings | `firings.jsonl` | `watch`, `firing`, `miss` | forever |

Unsuffixed `events.jsonl`, `retros.jsonl` and `feedback.jsonl` from older versions are still read, as the oldest. Old files are deleted when a log's first record of a new month is written; feedback lines still needed are first carried, unchanged, into the current month's file.

`factory log` prints records oldest file first, where `since <= ts < until`; a bound is a `ts` or a prefix of one, at least `YYYY-MM`.

**Events** add `event` (the hook's name, or `TaskStarted`, `TaskRemoved`) and:

| `event` | Fields |
|---|---|
| `SessionStart` | `source` |
| `SessionEnd` | `reason` |
| `UserPromptSubmit` | `from`: `human`, `subagent`, `session` or `notification` |
| `Stop` | `background_tasks` (a count) |
| `SubagentStart`, `SubagentStop` | `agent_id` |
| `Notification` | `notification_type`, `message` |
| `PermissionRequest` | `tool_name` |
| `PermissionDenied` | `tool_name`, `reason` |
| `PostToolUse` (`SendMessage`, `SubagentHandback`) | `tool_name`, `agent_id`; for `SendMessage` also `to` and `kind` (`QUESTION`, `BLOCKED`, `OVERTIME`, `SPLIT`, `DONE` or null) |
| `TaskStarted` | `base`, `lead_model` |
| `TaskRemoved` | |

**Feedback** is the outbox: issues agents report about the factory, and key decisions leads took without asking. An entry line adds `id`, `text`, `status: "open"`; a status line is only `id`, `ts`, `status` and optionally `pr`, and the latest one wins. Entries are filed from the section headed `Factory feedback` at the end of a subagent's report or a lead's `DONE:` message, one per list item (`none` files nothing); the same item filed twice is one entry. An entry whose text starts with `DECISION:` is a decision, any other an issue.

**Retros** add exactly these keys:
```json
{"defects": [{"caught_by": "tests|reviewer|lead|ci|human", "what": "..."}],
 "slowed": ["..."], "rules_missing": ["..."], "rules_dead_weight": ["..."], "brief_questions": ["..."]}
```

**Task state** adds `state` and, from the hand-over on, `deliverable`. A task's state is its last record.

## Site manager
Optional. `factory schedule install`, run in the main checkout on macOS, loads a launchd job (`~/Library/LaunchAgents/com.claude.factory.watch.<repository directory name>.plist`, output appended to `~/.claude/factory/watch.log`) that runs `factory watch` every 30 minutes; `remove` undoes it, `status` shows it. The job keeps the `PATH` and the dispatcher name of the moment it was installed (`gh` and `claude` must be on that `PATH`), so install again after renaming the dispatcher, and also when launchd refuses an install over a job still loaded. Install from the main checkout or the installed plugin: a job installed from a task worktree breaks when the worktree is removed. Elsewhere `install` exits 2 and prints the cron line to add by hand.

`health` evaluates the criteria and prints what fires now; it writes nothing. `watch` does the same, logs each new firing, and only when there is something new to send starts one headless Sonnet run (no shell, no file tools, 10 minute timeout, about $0.15) under `factory/roles/site-manager.md`. The run gets the evidence in its prompt and either messages the dispatcher (first line `ESCALATION: <repository directory name>`) or suppresses the firing with a reason. `always` firings cannot be suppressed. Nothing is escalated twice while its condition lasts; a suppression is looked at again after two hours. `dispatcher-missing` becomes a desktop notification instead, and holds everything else back until a dispatcher exists.

Criteria are `factory/criteria.json`, one entry per line: `name`, `escalate` (`always` or `judgment`), `threshold`, `what`. Delete a line to turn one off; an unknown name or a bad value is an error (exit 2). A criterion whose inputs cannot be read fires nothing and is printed as `not evaluated: <name>: <why>`.

| Criterion | Escalate | Threshold | Fires when |
|---|---|---|---|
| `past-limit` | always | | a lead is working past the time limit |
| `ci-red-default` | always | | a workflow's newest decisive run on the default branch did not succeed |
| `dispatcher-missing` | always | | tasks are live and not exactly one unfinished session carries the dispatcher's name |
| `quiet` | judgment | 20 min | a working lead wrote nothing and committed nothing for that long |
| `ended-undeclared` | judgment | 10 min | a lead's session ended with no hand-over or block declared |
| `blocked-unanswered` | judgment | 30 min | a task has been `blocked` that long |
| `handover-unacknowledged` | judgment | 24 h | a task was declared `handed-over` that long ago and is still in `review` |
| `review-loops` | judgment | 3 | a task had that many reviewer dispatches since it started |
| `wrong-model` | judgment | | a lead or subagent ran on a model other than the one pinned for its role |
| `ci-red-handover` | judgment | | a handed-over pull request has a failing check |
| `deep-stack` | judgment | 3 | a task's stack is deeper than that |
| `merged-not-cleaned` | judgment | 2 h | a task was merged or settled that long ago and not cleaned |

The dispatcher closes the loop: `firing <id> acted|ignored` on each escalation, `miss` for a problem that was not escalated (`--firing` when it had fired and was suppressed). `firings` lists the log, then precision `acted/(acted+ignored)` and recall `acted/(acted+missed)`.

`firings.jsonl` records carry only `ts`, `repo` and one of: a firing (`id`, `criterion`, `escalate`, `slug`, `subject`, `detail`); a decision (`id`, `decision`: `escalated` or `suppressed`, `reason`, `by`: `site-manager` or `watch`); an outcome (`id`, `outcome`); a clearing (`id`, `cleared: true`); a miss (`miss`, `slug`, `firing`).

## Known limits
- Nothing caps what a lead spends; the time limit is checked by the lead between subagent runs.
- A worktree made by hand under a slug used before inherits that slug's last declared state.
- A store nobody writes to is never pruned. A month file and `watch.log` have no size limit.
- The site manager's thresholds are first guesses; `quiet` reads file times, so a lead waiting on a long command looks quiet; a run that dies after sending may escalate the same firing again next tick.
- Two repositories whose directories share a name share a spec folder and a default dispatcher name.
