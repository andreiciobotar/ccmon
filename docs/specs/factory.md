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
- Unverified, to settle in the trial: how a plugin agent's `skills:` list names a skill from the same plugin (bare or `<plugin>:<name>`); the exact `state` value of a background session waiting on input; whether auto permission mode lets an unattended lead push a branch and open a pull request.
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
Run from inside a repo. Exit 0 on success, 2 on a usage or precondition error with one clear line on stderr. `--json` on `status`.

- `factory new <slug> (--brief <text> | --brief-file <path>) --dispatcher <session-name> [--base <slug>]`
  Validates the slug; refuses if the worktree, branch or a session of that name exists; refuses if the repo is at its cap, listing the open tasks. With `--base`, the task is stacked on an open task: the base task must exist, the worktree is created first with `git worktree add -b worktree-<slug> .claude/worktrees/<slug> worktree-<base>`, and the base branch is recorded in `git config branch.worktree-<slug>.factoryBase`. Without it the base is the default branch and Claude Code creates the worktree. Then starts the lead with the `claude --bg --worktree` command above, adding `--plugin-dir <this plugin>`, the model and permission mode from settings, and `factory/roles/lead.md` as the appended system prompt. The initial prompt carries the slug, the dispatcher's session name, the base branch, the start time (UTC), the time limit and the brief. Prints the session id and name.
- `factory status [--json]`
  One row per task worktree (directories under `.claude/worktrees/` not named `agent-*`): slug, branch, base branch (the recorded `factoryBase`, else the default branch), session state (from `claude agents --json --all`, matched by worktree path; `none` if no session), time since the session started and `overtime` when a session that is still working has passed the time limit, uncommitted changes, commits ahead of and behind its base, time of last commit, pull request state and URL (`gh`, when available; otherwise `unknown`), and `stale` when the last commit and the session's last activity are both older than 7 days. Also the count against the cap, and separately the number of leftover `agent-*` worktrees and of worktrees registered outside `.claude/worktrees/`.
- `factory clean [--dry-run]`
  Removes a task only when all hold: no uncommitted changes, branch fully merged into the default branch or its pull request merged, nothing unpushed that is not merged, and no open task is stacked on it. Merged into another task's branch does not count. Removal is `claude rm <id>` when a session exists, else `git worktree remove` plus branch delete. Never forces. Prints what it removed and, for each task it kept, the reason.
- Settings: `~/.claude/factory/settings.json`, all optional: `worktree_cap` (default 5, per repo), `lead_model` (default `fable`), `lead_permission_mode` (default `auto`), `task_time_limit_hours` (default 2). A missing or unreadable file means defaults; an invalid value is a precondition error naming the key. The dashboard will edit this file later.

One function builds the lead's launch command. That is the only place a different runner (another provider's CLI) would plug in; no runner abstraction is built now.

## Relay
Native session messaging only.
- Lead to dispatcher: `SendMessage` to the dispatcher's session name. The first line starts with one of `QUESTION:`, `BLOCKED:`, `OVERTIME:`, `SPLIT:`, `DONE:`, followed by the slug. A lead that asks a question ends its turn and waits.
- Dispatcher to human: every `QUESTION`, `BLOCKED`, `OVERTIME` and `SPLIT` is shown in the lead's own words, with the slug, never summarised away or answered on the human's behalf.
- Human to lead: the dispatcher sends the human's answer or question to the lead by slug, in the human's words plus any context the lead lacks.
- The human can always `claude attach <slug>` and talk to a lead directly.
- The dispatcher holds no state of its own: everything it knows is recoverable from `factory status` and the leads. It uses a stable session name per repo (`dispatcher-<repo>`), and on start it sweeps: any lead that is idle without a pull request is asked what it needs. A lead whose message cannot be delivered ends its turn with that message as its final output and waits.

## Time limit
A task that has run longer than the limit (2 hours by default, counted from the session's start) is paused and surfaced.
- The lead owns the call: at every checkpoint (each time a subagent returns, and before starting one) it checks the clock. Past the limit, or earlier when it judges the task is not converging (the same issue back from review a third time, a plan that keeps growing), it starts no new work and sends `OVERTIME:` with diagnostics: what is done, what is left, why it is slow, what it has cost so far if it can tell, and the options (continue with a new deadline, cut scope, split, abandon) with its recommendation. Then it waits.
- The dispatcher is the backstop: `factory status` flags `overtime`; a flagged lead that has not reported is told to pause and report, and one that does not respond is stopped with `claude stop` (resumable) and surfaced.
- Known limit: the lead can only check between subagent runs, so a pause can lag by the length of one run.

## Stacked tasks
A task may build on another open task instead of waiting for its pull request to merge.
- The stacked task's worktree branches from the base task's branch, its lead reconciles with that branch (not the default branch) when landing, and its pull request targets that branch.
- When a base task changes after review, the dispatcher tells the leads stacked on it to rebase onto the new tip and rerun their tests; when a base merges, they rebase onto the default branch and retarget their pull requests.
- The dispatcher keeps stacks shallow (three deep at most, as a rule of thumb) and lands them bottom-up.

## Lead lifecycle
1. **Plan:** read the brief and the repo; write `docs/specs/<slug>.md` including the facts it depends on; get a full count for any fact that rests on a sample; choose the approach a strong practitioner would choose today, checked against current documentation and prior art rather than assumed.
2. **Build:** coder against the spec file, one writer in the worktree at a time. Work that splits cleanly along an interface written in the spec may use parallel coders in child worktrees (`isolation: worktree`), merged back by the lead.
3. **Review:** full review by the reviewer after each implementation; a commit per coder round; blocker fixes re-reviewed by a fresh reviewer on the commit range; minor-only rounds checked by the lead.
4. **Quality assessment** (the lead's own, beyond the reviewer): state of the art, blind spots, regressions, undocumented behavior, simplicity, operability. Each item states what was verified, how, and what remains unverified.
5. **Land:** fetch; rebase onto the default branch; run the full tests; final review scaled to what the default branch did (unchanged: none; changed without overlap and tests pass: light; conflicts or overlap: full review of the affected area); push; open a draft pull request against the task's base whose body carries the spec summary, the review verdict, the test results and the quality assessment; send `DONE:` with the URL. If pushing or opening the pull request is denied, the lead writes the pull request body to `docs/specs/<slug>.pr.md`, commits it and sends `BLOCKED:`; the dispatcher shows the human and, on their word, pushes and opens the pull request from the attended session.
A task that turns out to be several is reported with `SPLIT:` and one brief per proposed task; the dispatcher starts them.

## Dispatcher's critique duty
The dispatcher watches how the factory performs (lead reports, `factory status`, and `ccmon --json` when installed) and tells the human what to change: where review loops pile up, which briefs split badly, which rules leads trip over, what the tokens bought. It proposes edits to the plugin's files; the human decides.

## Tests
- Slug validation, both sides of every rule.
- `new`: cap refusal, name collisions, `--base` (unknown base refused, the worktree and config commands issued, base recorded), the exact launch command for given settings (the `claude` and `git` calls go through one injectable runner so tests assert the argv without starting sessions).
- `status`: rows built from canned `git worktree list --porcelain`, `claude agents --json` and `gh` outputs, including `agent-*` and outside worktrees, a missing `gh`, the recorded base, the overtime flag on both sides of the limit, and the stale rule.
- `clean`: every keep reason (including a task with another stacked on it), the two removal paths, `--dry-run` changes nothing.
- Settings: defaults, overrides, invalid values.
- One end-to-end test in a temporary git repo with a real `git` and a fake `claude` on PATH.

## Out of scope here
Dashboard changes (task grouping, needs-you state, the cap setting), other providers, removing this repo's `.claude/agents` copies.
