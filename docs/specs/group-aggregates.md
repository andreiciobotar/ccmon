# Spec: group aggregates that read at a glance, and a table that uses its width

The human: "now that the agents are grouped in worktrees, id like to also see aggregate stats by group, the view is much narrower now."

## Facts this depends on (checked on main at 863d651)
- Repository and task rows already sum their sessions (`sumTotals(r.group)` in `internal/tui/view_dash.go` `treeLine`): tokens, main│sub bar, dispatches, signal pills. What is missing is not the sums but their legibility: a group row looks like a session row, has no session count and no live state, signals are free-floating pills that do not line up, and a group of one session is two rows with identical numbers.
- Where the width goes (`tableCols`, `dashLayout`): the name column absorbs all slack (31 cells at 140 columns, mostly blank), the signals area is mostly a lone `·`, and from 100 to 119 columns the 32-cell side column squeezes the table until signals drop out entirely (at 100x30 the table has no signals, at 80x24 it has them).
- `ccmon sessions --json` already carries per-group totals in `repos[]` and `repos[].tasks[]` (`ccmon/metrics.py` `totals()`): `session_count`, `tokens` (with `subagent_pct`), `dispatch_count`, `repeats`, `review_loops`, `recheck` (`rate` from summed counts), `warn_count`, `malformed`, `by_agent`, `by_model`. Every aggregate this spec shows except live state is there. The JSON does not change.
- The dashboard cannot read group totals from `repos`: its groups follow the filter and are updated per session between full reloads (`mergeSession`). So Go keeps summing sessions for display, and a parity test pins those sums to the JSON.

## Decisions (lead's, one line each)
- Rates aggregate from summed counts, never as a mean of rates: subagent share = Σsub / Σtokens, re-check = Σrechecked / Σcandidates. (Same rule as Python `totals()`.)
- Signals become fixed right-aligned columns instead of pills, so a column of numbers can be read down across repo, task and session rows.
- A group whose whole content is one session collapses into that session's row (no key, no state). Collapsing larger groups needs selectable group rows and a key; not done here (selection is by session id everywhere).
- The side column of mix boxes starts at 120 columns, not 100; below that the mixes are the one-line strips and the table has the full width.
- Aggregates are computed where they are shown: Python for the CLI/JSON, Go for the dashboard; a Go test asserts they agree on both fixture roots.

## Dashboard changes (Go, `internal/tui/`; do not edit `theme.go`)

### Rows (`state.go` `rows()`, `treeRow`)
- `treeRow` gains a row kind (repository, task, session) replacing `task bool`, and `suffix string` (rendered faint after the name).
- Repository and task rows with more than one session, or a repository whose content is not a single task-less session: suffix `×N` (N = sessions in the group). They stay unselectable.
- **Merged rows:** a task with exactly one session renders as one row: kind task, `s` set (selectable, flashes, opens on enter), name = task name, suffix = 8-character id, depth 1. A repository whose only session has no task renders as one row the same way (kind repository, depth 0). A repository whose only session is in a task is a repository row plus one merged task row.
- Everything keyed on `r.s != nil` (cursor, scrolling, visible list) keeps working; `shownGroups` counts by kind so merged rows still count as a repo or task. Sorting is unchanged.

### Columns (`view_dash.go` `tableCols`, `header`, `treeLine`)
Left to right, one space between columns:

| column | width | content |
|---|---|---|
| marker, dot, space | 3 | dot: session rows as today; group rows a green pulsing dot when any session in the group is live, blank otherwise; merged rows as a session |
| `REPO / TASK` | name | indent (2 per depth) + name + space + faint suffix |
| `ACTIVE`/`STARTED` | 11 | as today (hidden when inner width < 60) |
| `TOKENS` | 7 | as today |
| `MAIN│SUB` | 8..16 | the stack bar |
| `SUB%` | 4 | Σsub/Σtokens as `42%`, muted; `-` when tokens are 0 |
| `DISP` | 4 | as today |
| `REP` | 3 | repeats, yellow; faint `·` when 0 |
| `LOOP` | 4 | review loops, yellow; faint `·` when 0 |
| `RCHK` | 4 | re-check rate `67%`: orange when > 0, muted `0%` when there are candidates but none rechecked, faint `·` with no candidates |
| `WARN` | 4 | model warnings, red bold; faint `·` when 0 |
| `BAD` | 3 | malformed lines, faint; the column exists only when some visible session has malformed > 0 |

Headers are right-aligned over right-aligned columns. `signalPills` is deleted (and `flagPills` only if nothing else uses it).

Width allocation, inner width `iw`, `want` = longest `2*depth + name + 1 + suffix` over all rows, clamped 14..30:
1. Always: lead, name, time (if `iw >= 60`), TOKENS, MAIN│SUB at 8, DISP.
2. The four signal columns (18 + 1 gap) are shown when the name column would still get at least 14 cells.
3. `SUB%` is added only if the signals are shown and the name column still gets `want`; `BAD` (when it exists) only if `SUB%` is shown and the name still gets `want`. The column set therefore grows monotonically with width: signals, then `SUB%`, then `BAD`.
4. name = min(want, what is left). Slack beyond that goes first to the MAIN│SUB bar (up to 16), then to the name column (up to 30); anything further stays blank at the right edge.
A truncated name loses its suffix first (plain `cell` truncation with the ellipsis is fine).

### Name styling
Repository names bold text, task names purple (as today), session ids faint; suffix faint. On a selected (merged or session) row the name is bold; a merged task row keeps purple when selected. No-color output must stay readable: hierarchy by indent, `>` marker, `×N`.

### Layout (`dashLayout`)
`sideW` is 38 from 120 columns, 0 (strips) below. The 32-cell variant goes.

## Tests
- Go view tests with synthetic sessions: every signal column's value starts at the same cell on a repo, a task and a session row (non-zero repeats, loops, recheck, warns, malformed); zero shows `·`; `BAD` column absent with no malformed; `SUB%` and recheck of a group come from summed counts (two sessions with different shares/rates, expected value differs from the mean of rates).
- Column drop order at inner widths that exercise each step (signals gone, SUB% gone, all present); no line wider than the table at any width 60..160.
- Rows: merged task, merged repository, repository with one tasked session, `×N` suffix; cursor/enter on a merged row opens its session; `shownGroups` counts unchanged for the grouping fixture (3 repos, 4 tasks).
- Group live dot: a group with one live session shows the dot, without it does not.
- Parity (`internal/data` or `internal/tui`, wherever the Python runner is already used in tests): run `sessions --json` on `tests/fixtures/grouping` and `tests/fixtures/projects`; for every repo and task in `repos`, Go's `sumTotals` over the same session ids equals the JSON totals for tokens total/main/subagents, dispatch_count, repeats, review_loops, recheck counts, warn_count, malformed, and the displayed SUB% and RCHK strings equal the ones derived from JSON `subagent_pct` and `recheck.rate`.
- Goldens regenerated; add `grouping-dashboard-100x30.golden`.
- `make test` passes.

## Docs
`docs/specs/tui.md` (Screens → Dashboard: columns, merged rows, `×N`, group live dot, side column from 120) and the README's dashboard section updated to match. README states: group numbers in the dashboard follow the filter and range; the same totals are in `sessions --json` under `repos`; live state is dashboard-only.

## Done when
`make screenshot` at 140x40, 100x30 and 80x24 on the real corpus shows: group rows distinguishable with `×N`, signals as aligned columns present at all three sizes, no duplicated single-session rows, no large blank gap between names and numbers.

## Not in scope
New keys or collapsible groups, JSON or CLI table changes, kaizen stats, money, `factory/`, `theme.go`.
