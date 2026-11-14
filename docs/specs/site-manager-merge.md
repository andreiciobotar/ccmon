# Spec: the site manager's merge job

Merging a pull request is mechanical, so a deterministic command does it: `factory merge`. No model takes part in a merge. Two things start it: (a) the dispatcher runs it on the human's word; (b) the human comments `lgtm` on the pull request and the 30-minute `factory watch` tick merges it. Whatever is not mechanical stops the merge and is reported; the watch job never merges on judgment.

Stacked on `site-manager` (`docs/specs/site-manager.md`): this reuses its `gather`, `gh_json`, `NotEvaluated`, criteria, firing log and `watch`.

## What (b) rests on
Agents act with the human's own GitHub credentials, so **nothing technical separates the human's `lgtm` from one an agent posts**. Trigger (b) rests entirely on a rule: no role (lead, coder, explorer, reviewer, dispatcher, site manager) ever posts such a comment. The rule is written into every role file (below). The human accepts this while they are the only GitHub user. A separate GitHub identity for the factory, so that the check becomes technical, is a later refinement and not built here.

## Facts this depends on
Checked against `andreiciobotar/isshoni` with `gh` 2.80.0 on 2026-11-14.
1. **When a head last changed.** `gh api -X GET repos/{owner}/{repo}/activity -f ref=refs/heads/<branch> -F per_page=1` returns the branch's newest activity first: `{"before", "after", "timestamp", "activity_type"}` (`push`, `force_push`, `branch_creation`, `pr_merge`, ...). `timestamp` is when GitHub received the change, not a commit date. Seen: `worktree-site-manager` (`branch_creation`, `after` equal to the pull request's `headRefOid`) and `main` (four `pr_merge` entries, newest first).
2. **Write access.** `gh api repos/{owner}/{repo}/collaborators/<login>/permission` answers `{"permission": "admin"|"write"|"read"|"none"}`: `admin` for the owner, `read` for a stranger (`octocat`) on this public repo.
3. **Comments.** `gh api repos/{owner}/{repo}/issues/<n>/comments` rows carry `body`, `created_at`, `html_url`, `user.login`. The human's three real `lgtm` comments (on #6, #8, #9) have body exactly `lgtm`. Review comments and review bodies are other endpoints and do not count.
4. `gh pr list --state open --limit 100 --json number,url,isDraft,headRefName,headRefOid,baseRefName,isCrossRepository` and `gh pr view <n> --json mergeable` (`MERGEABLE`, `CONFLICTING`, or `UNKNOWN` while GitHub computes it: #14 answered `UNKNOWN` on a first call and `CONFLICTING` a minute later).
5. `gh pr checks <n> --json name,bucket`: buckets `pass`, `fail`, `pending`, `skipping`, `cancel`; exits non-zero on a failure but prints the JSON; prints no JSON when there are no checks (site-manager spec, fact 6).
6. `gh pr merge <n> --merge --match-head-commit <sha>` refuses when the head is no longer `<sha>`. The repo allows merge commits, has no branch protection, no auto-merge, and does not delete head branches on merge.
7. With merge commits a branch stacked on a merged one stays valid: retargeted onto the default branch, its pull request shows only its own commits. Squash would not (lesson from merging #1 to #7 by hand).
8. CI (`.github/workflows/test.yml`) runs on `pull_request` with the default types, so retargeting does not rerun it.
9. On `main` (not yet in this base), PR #12 made `collect` work out whether a recorded base still counts, so nobody clears `factoryBase`; rows there carry `stacked_on` (the recorded base or None).

## `factory merge <pr>... | --all [--dry-run]`
`<pr>` is a pull request number; one or more, or `--all` (not both, one required). Run from anywhere in the repo.

1. Gather: `gather(repo, None)` (sessions, rows, declared states) and the open pull requests (fact 4). Sessions that cannot be listed, or `gh pr list` failing: exit 1, nothing done. A named number that is not an open pull request: exit 2, nothing done.
2. Select. Named: those. `--all`: every open pull request whose task is handed over; each other one prints `#N skipped: <the hand-over reason>` and does not count as a stop (work in motion is not up for merging).
3. Order bottom-up: a pull request whose base is the head branch of another selected one comes after it; otherwise by number.
4. For each, in order, the steps below. The first blocker stops the run: print `#N stopped: <reason>`, then `#M not attempted` for each one left, and exit 1 after step 5.

Per pull request (each blocker has a kind, used by the watch job):
- `fork`: its head is in another repository.
- `handover`: no task row has its head branch; or that row's phase is not `review` (reason names the phase and declared state; `review` already means declared `handed-over`, or an undeclared task with an open pull request, with every session of the task finished); or the row's local head differs from the pull request's head (commits that are not on the pull request).
- `base`: its base is not the default branch and is not the head branch of a pull request merged into the default branch (`pull_request(repo, base)`), nor of one merged earlier in this run.
- `ci`: `gh pr checks` prints no JSON (no checks, or `gh` failed: the reason carries what it said), or some check is in bucket `fail` or `cancel` (reason names them), or no check is in bucket `pass`.
- `pending`: some check is in bucket `pending` and none failed.
- Then act: if its base is not the default branch, `gh pr edit <n> --base <default>` (retarget).
- `conflict` / `pending`: `gh pr view <n> --json mergeable`, asked up to `MERGEABLE_TRIES` (5) times `MERGEABLE_WAIT` (3) seconds apart while it answers `UNKNOWN`: `CONFLICTING` is a `conflict`; still `UNKNOWN` is `pending`; `gh` failing is `merge`.
- If it is a draft, `gh pr ready <n>`. Then `gh pr merge <n> --merge --match-head-commit <headRefOid from step 1>`. A failing `gh pr edit`, `gh pr ready` or `gh pr merge` is a blocker of kind `merge` whose reason is the command and its error.
- Print `#N merged <url>`.

5. If anything merged, run `clean` (the same code as `factory clean`) and print its lines.

`--dry-run` changes nothing on GitHub or disk: the read-only checks run, a stop does not end the run (later ones are checked as if earlier ones had merged), and each pull request prints `#N would merge` followed by ` (retarget onto <default> first; mergeability is checked after that)` when its base is not the default branch (the mergeable check is skipped for it) and ` (draft: would be marked ready)`, or `#N would stop: <reason>`. No clean. Exit 1 if any would stop.

Exit codes: 0 all merged (or would); 1 a stop or a failed step; 2 usage.

Not done, on purpose:
- **Recorded bases are not cleared**: fact 9 made that unnecessary.
- **Open pull requests stacked on a merged one are not retargeted at once**: each is retargeted when its own turn to merge comes (one path). Until then it shows against its base's branch, which stays on the remote.
- **Leads are not messaged by this command** (a script cannot message a session). A working lead on a merged base is flagged by the `base-merged` criterion below, whoever merged and however.

### `clean` guard
`keep_reason` gains one rule, first: a task whose worktree contains the running plugin (`PLUGIN_ROOT` inside the row's path) is kept, `this factory runs from it; leads started by it load their plugin from there`. `merge` runs `clean` unattended, and removing that worktree breaks every lead launched from it.

### `collect`
Adds `row["stacked_on"] = recorded or None`, the same line as on `main` (fact 9), so that the criterion below reads the same field before and after `main` is merged in.

## Trigger (a): the dispatcher
The dispatcher runs `factory merge <pr>...` or `factory merge --all`, only when the human said to merge, and reports the lines. There is no model in between: the command is the site manager's merge job, and running it costs the dispatcher one command and a few lines. The human's word replaces the `lgtm` check; every other check applies.

## Trigger (b): `lgtm`, in `factory watch`
At the start of each tick, before the criteria, `watch` runs the lgtm step; `health` runs the same step read-only.

For every open pull request (fact 4), in bottom-up order:
1. Its comments: `gh api --paginate --slurp repos/{owner}/{repo}/issues/<n>/comments`. An approval is a comment whose body, trimmed of whitespace and lower-cased, is exactly `lgtm`, written by a login whose permission (fact 2, asked once per login per tick) is `admin` or `write`. A sentence containing the word, and anyone else's comment, count for nothing and raise nothing. No approval: nothing happens to this pull request.
2. Take the newest approval by `created_at` (an edit does not renew a comment). Ask for the head branch's newest activity (fact 1). If there is none, or its `after` is not the pull request's `headRefOid`: blocker `head-time` (cannot tell when the head last changed). If the approval's `created_at` is not strictly later than the activity's `timestamp`: blocker `stale` (the head changed after the `lgtm`; a fresh one is needed). Neither merges.
3. Otherwise merge it exactly as `factory merge <n>` does (`watch`), or check it as `--dry-run` does (`health`). `--match-head-commit` pins the merge to the head the approval was checked against.
One pull request's blocker does not stop the others. `watch` prints `#N merged on lgtm <comment html_url>` or `#N blocked: <reason>`; after any merge it runs `clean`, prints its lines, and gathers the picture again so that the criteria see the merged state.

Results reach the dispatcher through the firing log, as two new criteria (two lines in `criteria.json`, two functions):

| name | escalate | fires for | when |
|---|---|---|---|
| `lgtm-blocked` | always | pull request | the lgtm step ended with a blocker other than `pending`. Subject `#<n>/<kind>`, slug of its task or null, detail `<reason> <url>`. A different kind is a different firing, so a red CI that turns into a stale `lgtm` is escalated again. `pending` (CI running, mergeability not computed) fires nothing: the next tick tries again. |
| `base-merged` | always | task | phase `motion` or `blocked`, and its `stacked_on` branch has a pull request merged into the default branch (`pull_request`). Subject the slug; detail `its base <branch> merged (<url>): tell its lead to rebase onto <remote>/<default> and to open or retarget its pull request against <default>`. Uses `phased`. |

`lgtm-blocked` is not evaluated when `gh pr list`, a comments call, a permission call or an activity call fails; then nothing is merged for the pull requests not yet reached. The criterion reads the step's results from the picture (`watch` stores what it did, `health` what the dry run found), so the step runs once per tick.

The plain case (an approval, a green handed-over mergeable pull request) runs no model: it merges, cleans and prints. A model runs only for a firing, as for any other criterion; `always` firings are passed on unjudged.

## Rule files
- Every role file gains the rule, in its limits: never post a pull request comment that reads `lgtm` (any case, any spacing), and never ask anyone else to: that comment is the human's approval to merge, and nothing technical tells yours from theirs. Files: `factory/roles/lead.md`, `factory/roles/site-manager.md` (already says it; reworded to match), `factory/agents/{coder,explorer,reviewer}.md`, `factory/skills/dispatch/SKILL.md`.
- `factory/roles/site-manager.md` gains `## Job: merge`: the script merges, not you; what `lgtm-blocked` and `base-merged` firings mean; pass them on with the reason as the diagnosis; you never merge, retarget or approve.
- `factory/skills/dispatch/SKILL.md`: merging is `factory merge`, only on the human's word, `--dry-run` to preview, never `gh pr merge` by hand; a stop is shown to the human, not worked around; the human's `lgtm` comment merges without you at the next tick (needs `factory schedule install`); `lgtm-blocked` and `base-merged` escalations and what to do with each; the `lgtm` rule. The sentence "The site manager does not merge" goes, and the stacks sentence no longer says to clear recorded bases.
- `docs/specs/site-manager.md` is the base task's file and is not edited here.

## Tests (`factory/tests/test_merge.py`, standard library, the `FakeRun` harness; nothing real is run)
- Command: one pull request merges (the exact `gh` argv in order: checks, view, merge with `--merge --match-head-commit <sha>`); a draft is marked ready first; a stack of two named in reverse order merges bottom-up and the upper one is retargeted before its mergeable check; a base merged earlier (not in the run) is retargeted; a base that is open and not selected is a `base` stop.
- Each blocker, both sides: fork; no task row; phase `motion` and `blocked`; local head differs; no checks; a failed check; a cancelled check; only skipped checks; a pending check; conflicting; `UNKNOWN` five times (with `time.sleep` faked) and `UNKNOWN` then `MERGEABLE`; each failing `gh` step.
- A stop prints `stopped`, the rest `not attempted`, exit 1, later pull requests see no `gh pr merge`; clean runs after a merge and not when nothing merged.
- `--all` skips tasks not handed over without stopping; a named number that is not open exits 2; sessions unlistable exits 1.
- `--dry-run`: no `gh pr edit|ready|merge`, no clean; continues past a stop; the stacked wording.
- lgtm: body variants (`lgtm`, ` LGTM\n`, `lgtm!`, `lgtm, thanks`, `not lgtm`); a `read` commenter ignored; the newest approval is the one compared; approval before, equal to, and after the activity timestamp; activity empty or naming another sha; permission asked once per login.
- `watch`: an approved green pull request merges with no `claude` run and `clean` runs; a stale approval fires `lgtm-blocked` with subject `#n/stale`, is sent, and is not merged; `pending` fires nothing; a blocker of another kind on the next tick is a new firing and the old one clears; a merged pull request clears its firing; one blocked pull request does not stop another's merge; a failing comments call leaves `lgtm-blocked` not evaluated and merges nothing further.
- `health` performs no `gh pr edit|ready|merge` and writes nothing.
- `base-merged`: fires for `motion` and `blocked` with a merged recorded base; not for `review`, an open base, or no recorded base.
- `keep_reason`: the plugin's own worktree is kept.
- `collect` sets `stacked_on`.
- The shipped `criteria.json` still loads and names every criterion once (existing test).

## Live check (nothing real is merged)
- `factory merge --all --dry-run` and `factory merge 14 15 16 --dry-run` against the real open pull requests; `factory health` for the two new criteria; the output goes in the pull request.
- The approval parser against the human's real `lgtm` comments on #6, #8, #9 (read-only `gh` calls) and the activity call against real branches.
- Not run live: `gh pr edit --base`, `gh pr ready`, `gh pr merge`, and `clean` after a merge. They are exercised only through the faked `run`.

## Known limits
- (b) rests on a rule, not on a mechanism (above).
- CI is not rerun after a retarget (fact 8): a stacked pull request merges on the checks of its head against its old base. With merge commits and a base merged unchanged the tree is the same; if the default branch moved otherwise, it is not.
- GitHub does not test the merge result unless branch protection asks for it; "mergeable" means no conflict.
- A pull request with no task worktree (opened by hand, or already cleaned) is a `handover` stop: the human merges it.
- At most 100 open pull requests are seen.
- An `lgtm` on a pull request whose CI is still running waits for the next tick (up to 30 minutes).
- `factory merge` holds no lock: two runs at once (a tick and the dispatcher) can both try a merge; the second `gh pr merge` fails and is reported as a `merge` stop.
- The activity feed is the only source for when a head changed; if GitHub is late in recording a push, the head sha does not match and the pull request is blocked (`head-time`), never merged.
