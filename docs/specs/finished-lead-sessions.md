# Spec: status and clean recognise a lead whose process has exited

A lead whose process has exited is listed by `claude agents` with its `cwd` at the main checkout, so `factory` matched no session to its task: `status` showed session `none`, and `clean` took the `git worktree remove` path instead of `claude rm`. Stacked on `explicit-task-state` (PR #10).

## Facts this depends on
Counted in this repo on Claude Code 2.1.325, 2026-11-14, over all 17 sessions of `claude agents --json --all` and all 11 worktrees of `git worktree list --porcelain`.
- 1 background session has exited (`factory-events`): keys `id, sessionId, name, kind, state, cwd, startedAt`; `state: done`; no `pid`, no `status`; `cwd` is the main checkout. The other 9 background sessions (4 `idle`/`done`, 5 `busy`/`working`) all have `pid` and `status` and a `cwd` inside their worktree. All 7 interactive sessions have `pid`, `status`, no `id`, no `state`.
- The 4 `idle`/`done` leads still have a live process (`claude bg-spare`), so "finished turn" and "exited" are two shapes.
- 9 of 10 task worktrees carry `locked claude session <slug> (pid N start <date>)`; `N` is the session's `pid`. Sub-agent worktrees carry `locked claude agent <name> (pid N ...)` with the lead's pid. The one exited lead's worktree is **not** locked. So in this count no lock outlived its process; the brief reports that it happens (cap-work-in-motion lead, same day), which was not reproduced. Both cases are handled.
- `claude rm --help`: "If it's safe, remove a background session from the list and delete its worktree. Its conversation is kept ... Unlike `stop`, works on already-exited sessions." It refuses unpushed commits unless told otherwise.
- `~/.claude/jobs/<id>/state.json` records `worktreePath` for the exited session. Not used: it is an undocumented internal file.
- Not verified: a real `claude rm` of an exited lead, with or without a lock (it would destroy a task; cleanup is the dispatcher's).

## Decisions
- **Match by name and main checkout, not by lock or job file.** An exited lead belongs to task `<slug>` when `kind` is `background`, it has no `pid`, its `name` equals the slug and its `cwd` is the main checkout (real paths). Why: public `claude agents` fields only; the `cwd` test confines the non-unique name to this repo; the lock is absent exactly in the observed case.
- **Two exited sessions with the same name keep the task.** Which one owns the worktree cannot be told, and `claude rm` on the wrong one would report a removal that did not happen.
  The check lives in `session_block`, which the cap shares, so such a task also reads as `motion` and holds a slot until the stray session is removed with `claude rm <id>`. Accepted: one definition of "unfinished", and doubt holds a slot everywhere else too.
- **"Finished" gains the exited shape.** A background session is finished when `state` is `done` and either `status` is `idle`, or it has neither `status` nor `pid`. Anything else keeps, as before (so `state: done` with a `pid` and no `status` still keeps). One function owns this test for `clean`, the cap and the no-`cwd` guard.
- **A lock is checked last, and only when there is no session to remove through.** Why: with a session, `claude rm` owns the lock; without one, `git worktree remove` is known to fail, so it is a keep with instructions rather than a failed removal. Checked last so that the reason appears only when the task would otherwise go.
- `status` shows the exited session like any other: its id and `done`. No new column.

## Behavior
- `sessions_in(sessions, root, path)`: sessions whose `cwd` is `path` or inside it (unchanged), plus exited leads matched as above. Every caller uses it (`collect`, the recheck before removal, the cap in `new`, the collision check in `new`).
- `finished(s)`: the test above. `session_block` and `unplaced_block` use it. `keep_reason`'s rule 2 checks move, unchanged, into `settled_reason` so that the lock check can follow both rules. `session_block` also returns `more than one exited session could be its lead; cannot tell which owns the worktree` when two or more sessions without a `pid` are among a task's sessions.
- `parse_worktrees` records `locked`: `None` when unlocked, else the reason text (empty string when git gives none).
- `clean`, last check in `keep_reason`: the task has no background session id and its worktree is locked → `kept <slug>: its worktree is locked (<reason>) and no session is left to remove it through; if nothing uses it, run: git worktree unlock <path>`. Exit code 0, nothing run.
- Known limit: a background session started by hand in the main checkout under a task's slug, without `--worktree`, and since exited, is taken for that task's lead. `factory new` refuses a slug while any session carries that name, so this needs a deliberate manual start.

## Tests
- `status`: an exited lead gives the row its id and `done`, phase unaffected; the same session with another name, with a `cwd` in another repo, or with a `pid`, gives `none`.
- `clean`: exited lead → `claude rm <id>`, no `git worktree remove`; exited shape with a `pid` keeps (`status missing`); two exited sessions of the same name keep; locked with no session keeps with the unlock command, exit 0, nothing run (with and without a lock reason); locked with a session → `claude rm`; a lead that exits between listing and removal is still removed (the recheck matches it).
- `parse_worktrees`: `locked`, `locked <reason>`, unlocked.

## Out of scope
Removing through `git worktree unlock` automatically; reading `~/.claude/jobs`; a separate "exited" label in `status`.
