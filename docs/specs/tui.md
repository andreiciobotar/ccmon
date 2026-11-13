# Spec: `ccmon-tui` live dashboard

A live terminal dashboard over the `ccmon` CLI. `make up` builds and starts it. The bar is high on looks and feel: dark, HEY-inspired, delightful to use.

## Facts this depends on
- `python3 -m ccmon {sessions,session,agents} --json` is the only data source; shapes are defined by the dict builders in `ccmon/metrics.py`. Verified: `session --json` carries per-dispatch `start`, `returned_at`, `duration_ms`; `sessions --json` carries `start`, `end` and the transcript `path`.
- Bubble Tea v2, Lip Gloss v2 and Bubbles v2 live under `charm.land/...` and differ from v1. Check the package source in the module cache, not memory.
- Dispatch statuses come from the Python side. Unverified which values it can emit beyond `completed`, `running`, `no-run`, `orphan`: the corpus tally showed 6 `failed`, the round-1 review says `failed` cannot occur. Count the status values across the whole corpus before deleting or keeping the `failed` handling.
- A dispatch in a session that is no longer live can stay `running` forever in the data (the agent never finished).

## Stack and layout
- Go; `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `charm.land/bubbles/v2` (the libraries basecamp/hey-cli uses). No fsnotify.
- `go.mod` at the repo root (module `ccmon`), `cmd/ccmon-tui/main.go`, `internal/data/` (running the Python CLI, JSON types, watching), `internal/tui/`. Binary at `bin/ccmon-tui`, git-ignored.

## Data
- Shell out to the Python CLI with the working directory at the repo root, located via `CCMON_ROOT`, then the executable path. A clear on-screen error when python or the package is missing. No transcript parsing in Go. `--projects-dir` and `--agents-dir` pass through.
- Initial load of `sessions` and `agents` for the active range runs in the background. Nothing blocks the UI.
- Real time: one mechanism, a 1 s mtime poll over `<projects>/*/*.jsonl` and `<projects>/*/*/subagents/*.jsonl`. No file watches (round 1 held about 1,760 open descriptors). A changed or new session re-runs `session <id> --json` for that session only; a session whose transcript disappeared is dropped. At most one refresh in flight per session, through a single code path that every detail fetch uses; a change that arrives during a refresh triggers exactly one more afterwards. Results merge into the list and aggregates, and a slower full reload never overwrites a newer per-session result. The `agents` aggregate refreshes at a slower cadence, and a failed refresh waits for the next cadence instead of retrying at once. A session is live if its transcript changed in the last ~30 s.
- Every subprocess runs under a context that is cancelled on quit and has a timeout; starting a new load cancels the one it replaces. No Python process outlives the TUI, and a hung call never blocks later refreshes.
- `--projects-dir` and `--agents-dir` are made absolute against the user's working directory before use. The TUI works when started from any directory.
- Read-only towards `~/.claude`.

## Theme
Fixed dark truecolor palette, whole screen painted, `NO_COLOR` honored with a plain rendering.
- background `#12111C`, surface `#1B1A2B`, raised `#25243A`, border `#343350`
- text `#ECEAF7`, muted `#8E8BA8`, faint `#5C5A75`
- blue `#5E9BFF`, purple `#A78BFA`, pink `#FF6FAE`, yellow `#FFD34E`, teal `#3FD8C4`, green `#58D68D`, orange `#FF9F5A`, red `#FF5C6C`
- Blue → purple → pink gradient for the title.
- One stable color per agent type everywhere: main yellow, explorer teal, coder blue, reviewer pink, general-purpose purple, Explore green, Plan orange, fork faint lavender; others by name hash. Models get stable colors too.
- Green good, yellow caution (repeat, review-loop), red problems (WARN, failed). Flags are small pills.

## Screens
1. **Dashboard:** header (gradient title, range, pulsing live indicator with count, last-update clock); KPI tiles (total tokens, subagent share with gauge, dispatches, repeats, review loops, re-check rate, model warnings); sessions table (cursor, project, time, total tokens, main/subagent split bar, dispatches, signal pills, live dot); agent mix as bars and model mix as a stacked bar; a **Now running** panel.
   - The time column shows what the list is sorted by: last activity when sorted by recent, start otherwise, and its header says which.
   - Now running lists every running dispatch in live sessions: spinner, agent, project, description, elapsed time ticking, tokens so far. Empty state: "nothing running". This is the dashboard's live heart; it replaces the round-1 Activity chart and Hot spots box, which are deleted.
   - Agent mix and model mix are always visible: a side column from about 100 columns, a one-line strip each below that.
2. **Session:** header (id, project, span, totals, and one line of signals: repeats, review loops, warnings, re-check, malformed); stacked bar of tokens by agent; timeline; dispatch list (time, agent, model, tokens in/out/total, duration, flag pills, description; columns drop from the right-hand numeric ones first only when the width truly cannot hold them, and are all present at 140 columns); detail pane for the selected dispatch (description, models, four token classes, duration, status, flags, what it repeats and the score, the review-loop events it took part in, warnings). Live while the session runs. No separate Signals box.
   - Timeline: one lane per agent type, one bar per dispatch positioned by start and sized by duration. Dispatches of the same type that overlap in time stack on separate one-row sub-lanes, so two concurrent explorers are two bars. Lanes are one row per sub-lane, not padded. The selected dispatch's bar is highlighted. Running bars animate only while the session is live; a `running` dispatch in a session that is not live renders static and is labelled `unfinished`.
   - Sessions spanning more than one day show dates on the end time and axis labels.
3. **Agents:** per agent type: dispatches, models, the four token classes, p50/p90 tokens and durations as bars, repeats, review loops, warnings.

## Interaction
- `tab`/`shift+tab` or `1` `2` `3` switch views; `j`/`k` and arrows move; `g`/`G` jump; `enter` opens; `esc`/`backspace` goes back; `r` cycles the range (24h, 7d, 30d, all); `f` toggles follow mode (track the most recently active session); `s` cycles the sessions sort; `/` filters sessions; `?` help overlay; `q`/`ctrl+c` quits. A footer shows the relevant keys. Keys are defined once (Bubbles `key` bindings) and feed the handler, the footer and the help overlay. On the error screen, `r` retries the same range. No mouse capture, so the terminal's own text selection keeps working.
- Subtle motion from a single tick: numbers count to new values, a row flashes when its data updates, the live dot pulses, running dispatches show a spinner, short toasts for events. Animations advance by real elapsed time and start within one fast tick. Near-idle CPU when nothing moves.
- In no-color mode, a legend entry uses the same glyph as its bar segment.
- Resize-safe; a friendly "too small" screen; deliberate empty, loading, error and stale states; display-width truncation; no line wider than the terminal.

## Build
- `Makefile`: `install`, `up` (build then run), `build`, `test` (Python unittest plus Go), `snapshot`, `screenshot`.
- `make install` gets a fresh clone ready: checks that Go and Python 3.11+ are present (a clear message naming what is missing and how to get it, non-zero exit), downloads the Go modules, builds the binary, and reports whether Chrome is available for `make screenshot` (optional, never a failure). It installs nothing system-wide.
- Snapshot mode: `bin/ccmon-tui --snapshot <dashboard|session|agents|help> --size 140x40 [--session <id>] [--no-color]` renders one settled frame to stdout and exits. Session lookup is left to the Python CLI (ambiguous prefix is an error); a failed load exits non-zero.
- `make screenshot` renders a colored snapshot to a PNG so the look can be judged without a TTY.
- README section.

## Tests
- Data layer against the Python CLI and the repo fixture (`tests/fixtures`).
- Model logic: keys, bounds, range, sort, filter, follow, merge.
- Update-loop sequences with real messages: change, refresh, result (including an error, and interleaved with a follow or enter fetch) never leave more than one refresh in flight for a session; a failed agents refresh does not relaunch on the next tick; a slow full reload does not overwrite a newer session result.
- Rendering at several sizes: exact height, no over-wide line, empty data, no-color.
- Content: golden snapshots of each view from the fixture with a fixed clock at 140x40 and 80x24 (no-color), so missing columns or shifted cells fail a test.
- Formatting and the tween.
