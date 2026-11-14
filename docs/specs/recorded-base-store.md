# A recorded base that nobody has to clear

## Problem
`factory new --base` records a stacked task's base in shared git config (`branch.<branch>.factoryBase`). The lead rules told the lead to clear it once the base merged; worktree isolation refuses that write. Until someone outside a worktree cleared it, `factory status` showed a merged task as a base.

## Facts (probed in this task's worktree, 2026-11-14)
- Read is allowed: `git config --get branch.worktree-recorded-base-store.factoryBase` ran (exit 1, nothing recorded).
- Write and unset are refused, both with: "This session is isolated in the worktree …/recorded-base-store, but this command sets branch.worktree-recorded-base-store.factoryProbe in the configuration the shared checkout reads. Refusing to run it — a worktree-isolated session's git operations must target its own worktree. Pass it for one command with `git -c` if it's a setting that allows, or ask the person to set it."
- The state store is writable: `factory state working` printed `recorded-base-store: working`.
- `factoryBase` is written in one place (`cmd_new`, run by the dispatcher, which is not isolated) and read in one place (`collect`). Full grep of `factory/`, `docs/`, `README.md`.
- `clean` already ignores the recorded base of a task whose own pull request is merged into the default branch (PR #7), and deliberately keeps a merged base while a task stacked on it is unmerged.

## Design: derive, do not clear
The recorded base stays where it is and is never cleared. `collect` works out whether it still counts.

Chosen over moving the base into `tasks.jsonl` because that needs a new record kind, a new command for the lead, and a fallback read of the old config for tasks recorded the old way, and still leaves a step somebody can forget. Deriving adds one small function, no storage, no command and no migration, and removes a step from two roles.

In `collect`, per row:
- `stacked_on`: the recorded `factoryBase`, or `null`. Raw, never judged.
- `base`: the recorded base while it counts, else the default branch (`repo.base`). Ahead/behind are measured against this.
- A recorded base no longer counts when
  - it is the branch of a task worktree whose pull request is merged into the default branch; or
  - it is not the branch of any task worktree, and either no local branch of that name exists (`git rev-parse --verify --quiet refs/heads/<base>` fails) or its pull request (`gh`) is merged into the default branch.
  An unknown pull request state means it still counts.
- "Merged" means merged into the default branch, as everywhere else in `factory`. A base merged into another task's branch still counts.

`keep_reason` (`clean`) matches stacked tasks on `stacked_on` instead of `base`, so what `clean` removes is unchanged: a merged base is still kept while a task stacked on it is unmerged (its lead may need the local base branch for `git rebase --onto`).

## Docs
- `factory/roles/lead.md`: drop "clear the recorded base with `git config --unset …`" from the base-merged steps.
- `factory/skills/dispatch/SKILL.md`: drop "and clear their recorded base".
- `docs/specs/factory.md`: `status` (base column, `stacked_on`), conventions (no clearing), tests list.

## Tests (`factory/tests/test_factory.py`)
- `status`: recorded base shown while the base task's pull request is open, unknown or merged into another branch; default branch shown, and ahead/behind measured against it, when the base task is merged into the default branch, when the base branch does not exist, and when the base has no worktree but a merged pull request. `stacked_on` keeps the recorded value in each case.
- `clean`: the existing stacked tests pass unchanged.

## Non-goals
- Moving or deleting existing `factoryBase` config.
- Following a chain (a base merged into another task's branch).
