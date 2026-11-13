# Spec: `ccmon-tui` live dashboard

A live terminal dashboard over the `ccmon` CLI. `make up` builds and starts it. The bar is high on looks and feel: dark, HEY-inspired, delightful to use.

## Facts this depends on
- `python3 -m ccmon {sessions,session,agents} --json` is the only data source; shapes are defined by the dict builders in `ccmon/metrics.py`. Verified: `session --json` carries per-dispatch `start`, `returned_at`, `duration_ms`; `sessions --json` carries `start`, `end` and the transcript `path`.
- Bubble Tea v2, Lip Gloss v2 and Bubbles v2 live under `charm.land/...` and differ from v1. Check the package source in the module cache, not memory.

## Stack and layout
- Go; `charm.land/bubbletea/v2`, `charm.land/lipgloss/v2`, `charm.land/bubbles/v2`, `github.com/fsnotify/fsnotify` (the libraries basecamp/hey-cli uses).
- `go.mod` at the repo root (module `ccmon`), `cmd/ccmon-tui/main.go`, `internal/data/` (running the Python CLI, JSON types, watching), `internal/tui/`. Binary at `bin/ccmon-tui`, git-ignored.

## Data
- Shell out to the Python CLI with the working directory at the repo root, located via `CCMON_ROOT`, then the executable path. A clear on-screen error when python or the package is missing. No transcript parsing in Go. `--projects-dir` and `--agents-dir` pass through.
- Initial load of `sessions` and `agents` for the active range runs in the background. Nothing blocks the UI.
- Real time: fsnotify on the projects dir, session dirs and `subagents/` dirs, adding watches for new dirs, plus a slow mtime poll as a safety net. A change re-runs `session <id> --json` for that session only, debounced about 400 ms, at most one refresh in flight per session, merged into the list and aggregates. The `agents` aggregate refreshes at a slower cadence. A session is live if its transcript changed in the last ~30 s.
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
1. **Dashboard:** header (gradient title, range, pulsing live indicator with count, last-update clock); KPI tiles (total tokens, subagent share with gauge, dispatches, repeats, review loops, re-check rate, model warnings); sessions table (cursor, project, time, total tokens, main/subagent split bar, dispatches, signal pills, live dot); a panel with agent mix as bars and model mix as a stacked bar. Side panel on wide terminals, stacked or hidden on narrow ones.
2. **Session:** header (id, project, span, totals); stacked bar of tokens by agent; timeline with one lane per agent type, one bar per dispatch positioned by start and sized by duration, running dispatches animated; dispatch list (time, agent, model, tokens in/out/total, duration, flag pills, description); detail pane for the selected dispatch (description, models, four token classes, duration, status, flags, what it repeats and the score, warnings). Live while the session runs.
3. **Agents:** per agent type: dispatches, models, the four token classes, p50/p90 tokens and durations as bars, repeats, review loops, warnings.

## Interaction
- `tab`/`shift+tab` or `1` `2` `3` switch views; `j`/`k` and arrows move; `g`/`G` jump; `enter` opens; `esc`/`backspace` goes back; `r` cycles the range (24h, 7d, 30d, all); `f` toggles follow mode (track the most recently active session); `s` cycles the sessions sort; `/` filters sessions; `?` help overlay; `q`/`ctrl+c` quits. A footer shows the relevant keys.
- Subtle motion from a single tick: numbers count to new values, a row flashes when its data updates, the live dot pulses, running dispatches show a spinner, short toasts for events. Near-idle CPU when nothing moves.
- Resize-safe; a friendly "too small" screen; deliberate empty, loading, error and stale states; display-width truncation; no line wider than the terminal.

## Build
- `Makefile`: `up` (build then run), `build`, `test` (Python unittest plus Go), `snapshot`, `screenshot`.
- Snapshot mode: `bin/ccmon-tui --snapshot <dashboard|session|agents|help> --size 140x40 [--session <id>] [--no-color]` renders one settled frame to stdout and exits.
- `make screenshot` renders a colored snapshot to a PNG so the look can be judged without a TTY.
- README section.

## Tests
- Data layer against the Python CLI and the repo fixture (`tests/fixtures`).
- Model logic: keys, bounds, range, sort, filter, follow, merge, debounce.
- Rendering at several sizes: exact height, no over-wide line, empty data, no-color.
- Formatting and the tween.
