# Spec: group sessions by repository and task

The data model becomes repository → task → sessions → dispatches. A task is a git worktree a session worked in. The CLI and the dashboard show sessions under their repository and task, with per-task totals.

## Facts this depends on (full counts on this machine, 2026-11-14, 103 sessions)
- Main transcripts carry lines `{"type": "worktree-state", "worktreeSession": {...} | null, "sessionId": ...}`: 1,717 lines in 20 files; 3 are `null` (the session left its worktree). They have no `timestamp` and no `cwd`. No subagent transcript has one.
- All 1,714 non-null records have `worktreePath == <repo>/.claude/worktrees/<worktreeName>`. `originalCwd` is the repository in 1,510 and the worktree path itself in 204 (sessions started inside the worktree), so the repository must be taken from `worktreePath`, not `originalCwd`.
- No recorded `worktreeName` matches `agent-<hex>`; those subagent isolation worktrees appear only as `cwd` values of main sessions that stay in the repository.
- 6 sessions recorded more than one worktree. In 5 of them Claude Code filed the transcript under the project directory of the last one.
- Every session without `worktree-state` lines starts (`cwd` of its first user or assistant line) in a repository root or a temp directory, never under `.claude/worktrees/`. One session has no `cwd` at all.
- 50 of 70 worktree paths seen no longer exist on disk: nothing here may ask git or the file system about a worktree.
- `relocated` lines are not needed: the rules below use only `worktree-state` and the first `cwd`.

## Attribution (Python, `ccmon/transcript.py`; the only place that decides it)
A session belongs to exactly one repository and at most one task.
1. While scanning the main transcript, collect the `worktreePath` of each non-null `worktree-state` record whose `worktreeName` is not `agent-<hex>` (`agent-[0-9a-f]+`), in order, dropping consecutive duplicates.
2. The anchor is the **last** collected path; with none, the session's first `cwd`.
3. If the anchor contains `/.claude/worktrees/`, the repository path is what precedes the first occurrence and the task is the first path segment after it, unless that segment is `agent-<hex>` (then no task). A `worktree-state` path without that marker (worktrees made by hooks) gives repository `originalCwd` and task `worktreeName`. Otherwise the repository path is the anchor and there is no task.
4. With no anchor (no `cwd` anywhere), the repository path is null and the repository name is the project directory name, as `project` already does.
5. The repository name is the basename of the repository path. Groups are keyed by path; two repositories with the same basename are separate groups with the same name.

A session that switched worktrees is therefore counted whole under the last task it entered (where its work ended up, and where Claude Code files it). Its tokens are not split between tasks. The switch stays visible: `worktrees` lists every task name it entered, in order (a task that comes only from the first `cwd` is listed too, so a non-null `task` is always its last element).

Known limits, to state in the README: a session started in a subdirectory of a repository, outside any worktree, shows that subdirectory as its repository; review loops are detected inside one session, so a task's count is the sum over its sessions.

## JSON (add only; every existing field keeps its meaning, including `project`)
- Each session object (`sessions --json` elements and `session --json`) gains `repo` (name), `repo_path` (string or null), `task` (string or null), `worktrees` (list of task names entered, in order; empty when none).
- `sessions --json` gains top-level `repos`: a list ordered by repository name then path, each
  `{"repo", "repo_path", <totals>, "tasks": [{"task", <totals>}, ...]}` with tasks ordered by name. A repository's totals cover all its sessions, with or without a task. Sessions without a task appear in no `tasks` entry.
- `<totals>` is: `sessions` (ids, ordered by start), `session_count`, `start`, `end`, and the same keys with the same meaning as a session: `tokens`, `by_model`, `by_agent`, `dispatch_count`, `repeats`, `review_loops`, `recheck` (`rate` computed from the summed counts, null with no candidates), `warn_count`, `malformed`. One helper in `ccmon/metrics.py` computes these for a list of sessions and `session_summary` uses it for its own numbers, so a session and a group of one session cannot disagree.

## CLI
- `sessions` and `agents` accept `--repo NAME` and `--task NAME` (exact match on the names above; both may be given). `ccmon agents --repo ccmon --task ci-workflow` is the per-task table of tokens by agent and model.
- `sessions` table: grouped. The first column is `repo / task / session`: a repository row (its name), then its sessions without a task (indented two spaces, 8-character id), then each task row (indented two) followed by its sessions (indented four). Repository and task rows show totals in the same columns as a session row; `start (UTC)` is the earliest start. The `id` and `project` columns go; the other columns stay. Order: repositories and tasks by name, sessions by start. The closing line counts repositories and tasks too.
- `session` text view: the `project` line is replaced by `repo      <name> (<repo_path or project_dir>)` and `task      <task or ->`, with ` (also entered: a, b)` when `worktrees` holds other names.

## Dashboard (Go; attribution comes only from the JSON fields above)
- `data.Session` gains `Repo`, `RepoPath`, `Task` (empty when null). A session's label is `repo` or `repo/task`; it replaces `Project` in the breadcrumb, the session header and the Now running panel.
- The sessions table is a tree in the same box, same columns, first column headed `REPO / TASK`:
  - a repository row: name in bold, totals of its visible sessions (tokens, main│sub bar, dispatches, signal pills; the time column shows the latest activity when sorted by recent, the earliest start otherwise; no session id);
  - its sessions without a task, indented two, named by 8-character id;
  - each task row, indented two, in the purple used for projects elsewhere, with the task's totals; its sessions indented four, named by 8-character id.
  - The `SESSION` id column is removed (the id is now the row name). The name column fits the longest row name over all rows (14 to 30 cells); signals take what is left, up to 28 cells, and drop out below 10; the name column absorbs any slack.
- Only session rows are selectable: `j`/`k`, arrows, `g`/`G`, page keys, `enter`, follow mode and the row flash behave as before and skip group rows. Scrolling counts display rows; when the selection moves up to the first session of a group, the group rows directly above it stay in view. No new keys.
- Sort (`s`) applies at each level with the group's aggregate: recent = latest activity in the group, tokens = summed tokens, subagent share = share of the summed tokens; ties by latest activity, then name. Sessions without a task come before the repository's tasks.
- Filter (`/`) matches the label, the project and the id. KPI tiles, agent mix and model mix keep following the visible sessions, so filtering to `ccmon/ci-workflow` shows that task's tokens by agent and model.
- The table title stays `Sessions N`; the subheader gains the repository and task counts.
- Group totals reuse the existing `totals` computation, refactored to take a session list.
- Theme, fixed agent order and keys are unchanged. `docs/specs/tui.md` gets the tree described under Screens.

## Tests
- A new fixture root `tests/fixtures/grouping/` (the existing `tests/fixtures/projects` is not touched, so its tests and goldens only change where the layout changes): small hand-written transcripts with fixed timestamps covering: repo `alpha` with one session without a task, a task with two sessions, a session that entered two worktrees (attributed to the last, `worktrees` lists both), a session whose `originalCwd` is the worktree path, a session whose only recorded worktree is `agent-<hex>` (no task), a session with no `worktree-state` whose first `cwd` is inside a worktree; repo `beta` with one session; one session with no `cwd`. At least two sessions have dispatches with different agents and models so totals are not trivial.
- Python: attribution for each case; `repos` totals equal the sums of their sessions for every totals key; a group of one equals the session's own numbers; `--repo`/`--task`; the grouped table; existing JSON keys still present with the old values on the old fixture.
- Go: decoding the new fields; ordering under each sort; cursor movement skips group rows; scrolling keeps group rows above the first session; filter by label; golden snapshots of the dashboard from the grouping fixture at 140x40 and 80x24, and regenerated goldens for the old fixture.
- `make test` passes.

## Done when
On the real corpus: `ccmon` shows its tasks with totals equal to the sum of their sessions, the `agent` repository's worktree sessions sit under `agent`, and `make screenshot` of the dashboard is readable at 140x40 and 80x24.

## Not in scope
Cost weighting, the kaizen stats page, the "needs you" state, the cap setting, hook events, splitting one session's tokens between tasks.
