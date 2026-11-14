# Spec: the site manager, watch job

A cheap persona (Sonnet) that looks at the factory every 30 minutes and tells the dispatcher only what is worth telling. Criteria are evaluated by code; a model runs only when something new fired. Every firing and what became of it is logged, so that precision and recall of the escalations can be read off: that is the signal for downgrading the dispatcher.

The merge job is a separate task (`site-manager-merge`, stacked on this one). Nothing here merges, and the watch job is read-only apart from its own log.

## Facts this depends on
Checked on this machine (macOS, Claude Code 2.1.325), 2026-11-14.
1. **Scheduler.** A LaunchAgent loaded with `launchctl bootstrap gui/<uid> <plist>` (`StartInterval` 1800) ran a script with no model session and no terminal; inside it `gh auth status` was logged in, `claude agents --json` listed sessions, `claude -p --model haiku` answered, and `osascript -e 'display notification ...'` exited 0. `launchctl bootout gui/<uid>/<label>` removed it. The job sees only the `PATH` its plist gives it. There is no crontab on this machine.
2. **Fresh run.** `claude -p --model sonnet --permission-mode dontAsk --allowedTools "Bash(factory:*)" Read SendMessage ToolSearch ListAgents --plugin-dir <plugin> "<prompt>"` ran `factory status`, read a file, ran `git log` (read-only git is not denied in `dontAsk`), and its `SendMessage` to a session name arrived in that session as a `<cross-session-message>` (sent to `site-manager`, received from `site-manager-5f`). `SendMessage` is a deferred tool there: the run loads it with `ToolSearch`. The model used was `claude-sonnet-5-5`.
3. A first live tick let the run record its own decisions with `factory firing` under `--allowedTools "Bash(<factory> firing:*)"`. Sonnet wrote one compound shell command (a variable, a loop, `tail` on transcripts); `dontAsk` denied it, the run sent its escalation anyway and recorded nothing, so the next tick would have escalated again. Hence the design below: the run gets its evidence in the prompt, has no shell and no file tools, and returns its decisions as structured output that `watch` records. Verified: `claude -p --model sonnet --permission-mode dontAsk --allowedTools SendMessage ToolSearch --output-format json --json-schema '<schema>' "<prompt>"` sent its message to a named session and printed one JSON object whose `structured_output` matched the schema (also `is_error`, `total_cost_usd`: $0.15 for that run).
4. `claude agents --json --all` rows carry `name`, `sessionId`, `kind`, `status`, `state`, `cwd`; a lead's transcript is `~/.claude/projects/*/<sessionId>.jsonl` with its subagents under `<sessionId>/subagents/`.
5. `events.jsonl`, full count (172 lines, one repo): `SubagentStart` carries `role` (`reviewer` 10, `coder` 6, `explorer` 5) and `model` null on all 21; `SubagentStop` and `PostToolUse` carry the model (`claude-opus-5-5` for reviewer, `claude-sonnet-5-5` coder, `claude-haiku-5-5` explorer; never null for those roles); `TaskStarted` carries `lead_model` (alias, `fable`), lead events carry `claude-fable-5-2` or null. A pinned alias is a substring of the model id it resolves to.
6. `gh run list --branch main --limit 20 --json conclusion,status,url,workflowName` and `gh pr checks <url> --json name,bucket` (bucket `pass`, `fail`, `pending`, `skipping`, `cancel`) answer as expected; `gh pr checks` exits non-zero when a check fails but still prints the JSON.

## Criteria are data
`factory/criteria.json`: a JSON list, one object per line, `{"name", "escalate": "always"|"judgment", "threshold": number|null, "what": "<one sentence, shown to the site manager>"}`. Deleting a line turns a criterion off; changing `threshold` or `escalate` tunes it. A name the code does not know, a bad `escalate`, or a threshold that is not a positive number where one is needed is an error (exit 2) naming the entry.

Each criterion is one small function in `factory/bin/factory` that takes the gathered picture and returns firings. Shipped set (thresholds are first guesses from eight tasks):

| name | escalate | threshold | fires for | when |
|---|---|---|---|---|
| `past-limit` | always | - | task | its row's `overtime` is true (the same flag `status` shows: lead working past `task_time_limit_hours`) |
| `ci-red-default` | always | - | workflow | per workflow on the default branch, the newest completed run whose conclusion is not `cancelled`, `skipped` or `neutral` is not `success` |
| `dispatcher-missing` | always | - | repo | some task is in `motion`, `blocked` or `review`, and the number of unfinished sessions named as the dispatcher is not exactly one |
| `quiet` | judgment | 20 minutes | task | phase `motion`, lead session working (`status` busy or `state` working), and its last activity (below) is older than the threshold |
| `ended-undeclared` | judgment | 10 minutes | task | phase `motion`, sessions can be told and none is unfinished in it (the lead stopped without declaring a hand-over or a block), last activity older than the threshold |
| `blocked-unanswered` | judgment | 30 minutes | task | phase `blocked` and declared more than the threshold ago |
| `handover-unacknowledged` | judgment | 24 hours | task | phase `review`, declared `handed-over` more than the threshold ago |
| `review-loops` | judgment | 3 | task | phase `motion` or `blocked`, and at least threshold `SubagentStart` events with role `reviewer` for its slug since its last `TaskStarted` |
| `wrong-model` | judgment | - | task/role | for a task with a worktree: an event since its last `TaskStarted` with role `explorer`, `coder` or `reviewer` whose model is set and does not contain the `model:` pinned in `factory/agents/<role>.md`, or role `lead` whose model does not contain that `TaskStarted`'s `lead_model` |
| `ci-red-handover` | judgment | - | task | phase `review` with a pull request, and `gh pr checks` reports a check in bucket `fail` |
| `deep-stack` | judgment | 3 | task | the chain of tasks under it (its base is another task's branch, and so on), itself included, is longer than the threshold |
| `merged-not-cleaned` | judgment | 2 hours | task | phase `merged` and merged more than the threshold ago (`gh pr view <url> --json mergedAt`), or phase `settled` and settled more than the threshold ago |

"Last activity" is the newest of: transcript mtimes, last commit, the declared record's `ts`, the session's start.

Not shipped: cost far above the median landed task, and the lead doing most of the work itself. Both need cost per task, which `cost-in-money` is building; a second token count here would be a second owner. They become two lines in `criteria.json` and two functions when it lands.

A criterion whose inputs cannot be read (sessions not listable, `gh` failing, no event log) is not evaluated: it fires nothing and is reported as `not evaluated: <name>: <why>`. A criterion that reads a task's phase is also not evaluated when the phases cannot be trusted: an unfinished session has no working directory (`unplaced_block`), or the pull request state of a task on a branch is `unknown`; `collect` then falls back to `motion`, and acting on that would clear open firings and raise false ones. `gh` output of an unexpected shape (not the list or object asked for) counts as `gh` failing. In `ci-red-handover`, a pull request for which `gh pr checks` says `no checks reported` is skipped; any other failure of that call leaves the criterion not evaluated.

A firing is `{"criterion", "escalate", "slug" (null for repo-wide ones), "subject", "detail", "what"}`. `subject` identifies the thing within the criterion (the slug, `<slug>/<role>`, the workflow name, or `-`); `detail` is one line of evidence (numbers, URL, session id).

## Commands

### `factory health [--json] [--dispatcher <name>]`
Read-only; writes nothing. Gathers the picture once (`Repo`, `list_sessions`, `collect`, declared states, events, criteria) and prints one line per firing, `<criterion>  <always|judgment>  <subject>  <detail>`, or `nothing fired`; then one `not evaluated:` line per skipped criterion. `--json`: `{"firings": [...], "not_evaluated": [{"criterion", "why"}]}`. `--dispatcher` defaults to `dispatcher-<main checkout directory>`, as in `factory new`. Exit 0 whatever fired.

### `factory watch [--dispatcher <name>]`
The scheduled tick. In order:
1. Evaluate as `health` does.
2. Fold `firings.jsonl` (below) for this repo. A firing is **open** from its `fired` record until a `cleared` record.
3. For every open firing whose criterion was evaluated and that no longer fires: append `cleared`.
4. For every current firing, by `(criterion, subject)`:
   - no open firing: append `fired`; it is to be sent.
   - open, no decision yet (an earlier run died): to be sent again.
   - open, decided `suppressed` more than `RESURFACE_MINUTES` (120, a constant) ago: append `cleared` and a new `fired` whose detail ends with `; suppressed earlier: <reason>`; to be sent. So a suppression is looked at again every two hours while its condition lasts.
   - otherwise (escalated, or suppressed recently): nothing. An escalation is never repeated while its condition lasts.
5. Nothing to send: print `nothing new` and exit 0. **No model runs.**
6. `dispatcher-missing` is handled here, since no model can message a session that is not there: show a desktop notification (`osascript -e 'display notification "<detail>" with title "factory: <repo dir>"'`); on exit 0 append the decision `escalated`, by `watch`, reason `notified on this machine: no dispatcher session to message`; otherwise leave it undecided for the next tick. While it fires, nothing else is sent (it stays undecided and goes out on the first tick with a dispatcher) and no model runs.
7. Otherwise start one site manager run (below) with the firings to send and wait for it (10 minute timeout). From its structured output, append a decision (by `site-manager`) for each sent firing it names, except: `suppressed` for an `always` firing is not recorded (the bypass is enforced here, not left to the model); and when the output says the message was not sent, no `escalated` is recorded. Ids that were not sent are ignored. Then print one line per sent firing with its decision, or `undecided` (sent again next tick), and the run's cost. Exit 1 when the run failed to start, timed out, or printed no usable output; else 0.

Each tick prints a first line `<UTC time> watch <repo dir>` so the log file reads as a history.

### `factory firing <id> acted|ignored` — the dispatcher
Appends the outcome of an escalation: `acted` when the dispatcher or the human did something because of it, `ignored` when it was noise. `<id>` is an id or a unique prefix among this repo's firings; unknown or ambiguous is refused (exit 2), and so is a firing whose decision is not `escalated`. The last outcome wins.

### `factory miss <text> [--slug <slug>] [--firing <id>]` — the dispatcher
Records a problem the dispatcher or the human caught that the site manager had not escalated. `--firing` names the firing when one existed and was suppressed or undecided (the filter was wrong); without it nothing fired (a criterion is missing or too loose). Refused when `--firing` names an escalated firing: that is `factory firing <id> acted`.

### `factory firings [--json]`
This repo's firings, oldest first: `<id>  <ts>  <criterion>  <subject>  <decision or undecided>  <outcome or ->  <open|cleared>  <reason>`, then the misses, then the summary:
`escalated E (acted A, ignored I, no outcome N); suppressed S; missed M (F suppressed first, G never fired); precision A/(A+I); recall A/(A+M)`. A ratio with a zero denominator prints `-`. `--json`: `{"firings": [...folded...], "misses": [...], "summary": {...}}` with the same numbers (`precision` and `recall` as numbers or null).

### `factory schedule install [--dispatcher <name>] | remove | status`
Run inside the repo; one job per repo, label `com.claude.factory.watch.<main checkout directory>`.
- `install` writes `~/Library/LaunchAgents/<label>.plist` (`ProgramArguments`: the running Python, this `factory`, `watch`, `--dispatcher <name>`; `WorkingDirectory`: the main checkout; `StartInterval` 1800; `EnvironmentVariables.PATH`: the caller's `PATH`; output appended to `~/.claude/factory/watch.log`; no `RunAtLoad`), then `launchctl bootout gui/<uid>/<label>` (result ignored) and `launchctl bootstrap gui/<uid> <plist>`. Installing again replaces the job. It warns when this `factory` lives under `.claude/worktrees/`: the job breaks when that worktree is removed, so install from the main checkout or the installed plugin.
- `remove` boots the job out and deletes the plist; with nothing installed it says so and exits 0.
- `status` prints whether the plist exists and whether launchd has the job loaded (`launchctl print`).
- On anything but macOS, `install` exits 2 and prints the cron line to add by hand (`*/30 * * * * cd <repo> && <python> <factory> watch --dispatcher <name> >> ~/.claude/factory/watch.log 2>&1`).
The tests never run `launchctl`: `run` is faked and `HOME` is a temporary directory.

## The log: `~/.claude/factory/firings.jsonl`
Append-only, one file, written with `append()`. Every record has `ts` and `repo` (the main checkout's real path). Kinds:
- fired: `id`, `criterion`, `escalate`, `slug`, `subject`, `detail`. `id` is the first 12 hex digits of SHA-256 over repo, criterion, subject and ts joined by NUL.
- decision: `id`, `decision` (`escalated`|`suppressed`), `reason`, `by` (`site-manager`|`watch`).
- outcome: `id`, `outcome` (`acted`|`ignored`).
- cleared: `id`, `cleared: true`.
- miss: `miss` (the text), `slug`, `firing` (an id or null).
Folding: a firing is its `fired` record plus its first decision, last outcome and whether cleared. Lines that are not JSON objects, belong to another repo, or are not a miss and lack a string `id`, are skipped; a missing file is an empty log.

## The site manager run
`factory/roles/site-manager.md` holds the persona's rules. `watch` starts a run, in the main checkout, with (one function builds it, as `build_launch_argv` does for a lead):
```
claude -p --model sonnet --permission-mode dontAsk --allowedTools SendMessage ToolSearch
  --output-format json --json-schema '<schema>' --append-system-prompt <roles/site-manager.md> "<prompt>"
```
The model is pinned in code (`SITE_MANAGER_MODEL = "sonnet"`). The run has no shell and no file tools: it cannot change anything, and all it can do besides answering is message a session. Each run is fresh: it has no memory.

The prompt: `Job: watch. Repo: <root>. The dispatcher's session name is <name>.` and the firings to send as JSON. Each has `id`, `criterion`, `escalate`, `slug`, `subject`, `detail`, `what`, and for a task firing the evidence, gathered by `watch`:
- `task`: from its status row: `phase`, `state`, `session`, `elapsed` and `last_commit` as ages, `dirty`, `ahead`, `behind`, `pr_state`, `pr_url`, `overtime`.
- `transcript_tail`: the last 15 entries of the lead's transcript, read from its final 64 KiB, one string each: `<timestamp> <user|assistant>: ` then, per content block, the text, `[tool <name>] <input>` or `[result] <content>`, each cut to 300 characters. Absent when no transcript is found.

The schema: `{"sent": boolean, "decisions": [{"id", "decision": "escalated"|"suppressed", "reason"}]}`, all required. `sent` says whether the one `SendMessage` succeeded (true as well when nothing needed sending).

Rules in the role file: `always` firings are passed on unjudged; for a `judgment` firing weigh the evidence given and decide whether the dispatcher would act on it now; one `SendMessage` to the dispatcher per run, first line `ESCALATION: <repo dir>`, then one line per escalated firing `<id> <criterion> <subject>: <diagnosis in one sentence, with the evidence>`; never message a lead; when in doubt between noise and a real problem that costs money or blocks the human, escalate.

## The dispatcher
`factory/skills/dispatch/SKILL.md` gains a section: what the site manager is; `factory schedule install` on the human's word and `factory schedule status` in the on-start sweep; an `ESCALATION:` message is a teammate's report, checked against `factory status` and shown to the human when it needs them; after handling each escalated firing run `factory firing <id> acted|ignored`; whenever it or the human catches a problem that was not escalated run `factory miss`; `factory firings` is the record the human uses to decide the dispatcher's model.

## Shared code touched
- `run(argv, cwd=None, timeout=None)`: a timeout returns `(124, "", "timed out")`.
- Events are read through `read_log` (the month files of `event-log-bounds`), filtered to this repo and ordered by `ts`. `firings.jsonl` is written with `append()` to the one unsuffixed file, like `tasks.jsonl`: it is small and is never pruned, because it is the record the precision and recall are computed from.
- Sessions are matched to tasks with `sessions_in(sessions, root, path)` and judged finished with `finished()`, as `collect` does.
- Everything else is new functions and new subcommands; `collect`, `sessions_in`, `session_block`, `keep_reason`, the event writers and `read_feedback` are not edited (other open tasks edit them).
- `plugin.json` version is not bumped here: PR #8 and the base are changing it; the dispatcher sets it when they land.

## Tests (`factory/tests/test_health.py`, standard library, same harness style as `test_factory.py`)
- Criteria file: the shipped file loads; an unknown name, a bad `escalate`, a missing or non-positive threshold are refused; a deleted line turns its criterion off.
- Each criterion on both sides of its condition and its threshold, from a hand-built picture (rows, sessions, events, declared records, faked `gh` answers), including: `dispatcher-missing` with zero, one and two sessions and with no live task; `ci-red-default` with a newer cancelled run over an older failure and with two workflows; `wrong-model` with a null model and with the lead's model; `deep-stack` at and above the threshold; a slug reused (events before the last `TaskStarted` ignored).
- Not evaluated: sessions unlistable, `gh` failing or answering with the wrong shape, an unplaced session, an `unknown` pull request state, are reported and fire nothing; in `watch`, an open `blocked-unanswered` firing plus an unplaced session leaves no `cleared` record.
- `health`: table and `--json`; writes nothing to the store.
- `watch`: nothing fired starts no `claude`; a new firing is logged and sent; a firing already escalated is not sent again; an undecided one is sent again; a suppressed one is sent again only after `RESURFACE_MINUTES`; a condition that ended is cleared and fires anew later under a new id; a criterion that was not evaluated clears nothing; `dispatcher-missing` notifies, records `escalated` by `watch`, runs no `claude` and holds the other firings; a failed notification leaves it undecided; the launch argv (model, permission mode, exactly the two allowed tools, the schema, role text, prompt carrying the firings with `task` and `transcript_tail` built from a transcript file in the temporary home); decisions recorded from the run's output; `suppressed` for an `always` firing not recorded; `sent` false records no escalation; an id that was not sent ignored; output that is not the expected JSON exits 1 and records nothing; a timed-out run exits 1.
- `firing`: each refusal; prefix ids; outcomes only on escalated; the last outcome wins.
- `miss` and `firings`: the summary numbers, zero denominators, `--json`.
- `schedule`: the plist content (parsed with `plistlib`), the `launchctl` calls made, `remove` and `status`, the refusal off macOS, the worktree warning; `launchctl` is never really run.

## Live check
- `factory health` and `factory health --json` against this repo's tasks; the output goes in the pull request.
- `factory schedule install`, `status`, `remove` once on this machine, from this worktree, removed at once.
- One real `factory watch` tick with `--dispatcher` set to this lead's own session name, so that the Sonnet run's `ESCALATION:` lands here and not with the dispatcher, with two thresholds lowered for the tick so that something fires; then `factory firings`. The test log is moved out of the store afterwards. The pull request says exactly what was run.

## Known limits
- Thresholds are guesses; the log is how they get tuned.
- `quiet` reads file mtimes; a lead waiting on a long foreground command looks quiet. That is what the judgment step is for.
- `review-loops` counts reviewer dispatches, not `CHANGES_NEEDED` verdicts (the events do not carry verdicts).
- `dispatcher-missing` reaches the human only as a desktop notification on this machine.
- A site manager run costs about $0.15 (one measured run); `watch` prints each run's cost. The runs load no plugin, so the hooks record no events for them.
- If a run sends its message and then dies before answering, its escalations stay undecided and are sent again next tick: a duplicate is possible, a lost escalation is not.
- `watch.log` grows without bound (a few lines per tick).
- macOS only for the scheduler; elsewhere the cron line is printed, not installed.
