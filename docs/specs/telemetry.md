# Spec (draft): factory telemetry and kaizen

Status: requirements collected, not yet designed. To be completed after the first live factory trial, so it is shaped by what a real task produces. Nothing here is built.

## Purpose
Continuous improvement of the factory: measure flow, quality and cost per task, show it in a way people want to look at, and let the dispatcher propose and start improvements that are then checked against the numbers.

## Facts to establish first
- What a real factory task leaves behind: transcript lines, session messages between dispatcher and lead, hook events, pull request data. Count across the trial tasks, not samples.
- Transcripts record the repo, worktree path, name and branch (`worktree-state`, `relocated`, `cwd`); 50 of 70 worktree paths seen no longer exist on disk, so grouping must come from transcripts. 6 of 18 older sessions switched worktree mid-session.
- Token totals weight all four classes equally today; cache reads dominate and are the cheapest class.
- The "re-checked" metric counts only the Read tool, so checks done through Bash are missed.

## Collect
- Hook events shipped by the plugin, one line per event: session start and end, subagent finished, needs input, permission denied, worktree created and removed.
- A structured retrospective from each lead at the end of a task, stored outside the worktree: which stage caught each defect, what slowed the task, rules that were missing or dead weight, questions a better brief would have answered.
- An outbox for feedback about the factory itself, from every role (explorer, coder, reviewer, lead). Each agent's report format gains an optional last section, "Factory feedback": things about the brief, the rules, the tools or permissions that cost it time or made it guess, not things about the task. At most three items, each with what happened, what it cost and a suggested change; "none" is the expected answer most of the time, so the channel stays signal. Read-only agents cannot write files, so entries are filed from their final report (by a SubagentStop hook if that proves reliable, else by the lead, verbatim) into one append-only file outside the worktree, tagged with task, role, model and factory version. Each entry has a status (open, accepted, rejected, done) and, once acted on, the pull request that changed the rule. The dispatcher clusters open entries for a review with the human; the dashboard shows them as a board.
- Pull request outcomes and merge times from GitHub.
- The factory (plugin) version on every record, so before and after a rule change can be compared.
- Line stops: every `QUESTION`, `BLOCKED` and `OVERTIME`, with time to answer and a cause.

## Measures (few, acted on)
- Flow: brief to pull request, pull request to merge, working versus waiting and on whom, work in progress against the cap.
- Quality: first-pass yield at each gate (reviewer, lead's assessment, human review), defects by the stage that caught them including after merge, rework share of a task's cost.
- Cost: money per landed task, by role, from per-model prices kept in settings.

## Show
- Sessions grouped by repo and task in the existing views; a "needs you" state for leads waiting on the human; the worktree cap as an editable setting (default 5 per repo) written to the factory settings file.
- A global stats page for kaizen, across all repos. It should be something people enjoy opening, not a table of numbers:
  - charts over time for the measures above, with rule changes marked on the time axis so their effect is visible;
  - a value-stream view of a task's stages showing where time and money went, and the waste broken down by kind (rework, waiting, over-processing, overproduction, defects, inventory, motion);
  - line stops as a board: what stopped, for how long, why;
  - motion and colour in the dashboard's existing style, and small moments of reward for real improvements (a streak of tasks landed first-pass, a record low cost per task).

## Act
- The dispatcher runs a regular kaizen review: the biggest waste since the last one, a five-whys on the worst task, one or two proposed changes each with the number it should move.
- It may start an improvement itself as a normal factory task that edits the plugin's rules in a worktree and opens a pull request; the human approves at the merge. One change at a time; the next review checks whether the number moved.

## Cautions
- Numbers that become targets get gamed, by models too. Prefer measures of outcomes the human sees (merged work, defects found later) over ones a lead can move by skipping a step.
- Early samples are small; retrospectives and five-whys carry more than charts at first.
