# Spec: the monitor is called isshoni everywhere

The project is Isshoni; the monitor's commands were still `ccmon` and `ccmon-tui`, and the README had to explain that. After this task there is one name.

## Facts (counted on main at e2aa114; the script ran again after main at ae16ff4 was merged in)
- `git grep -i ccmon`: 134 lines in 50 tracked files; 9 tracked paths contain `ccmon`.
- The only spellings are `ccmon` and `CCMON` (in `CCMON_ROOT`).
- Nothing under `factory/` runs the monitor: its two mentions are prose in `factory/skills/dispatch/SKILL.md`. No hook calls it. The user's shell profile and `~/.claude/settings.json` do not mention it. So no compatibility alias is needed.
- Golden snapshots are regenerated with `go test ./internal/tui -update`.

## Names
| Old | New |
|---|---|
| `bin/ccmon`, `python3 -m ccmon`, package `ccmon/` | `bin/isshoni`, `python3 -m isshoni`, `isshoni/` |
| `bin/ccmon-tui`, `cmd/ccmon-tui` | `bin/isshoni-tui`, `cmd/isshoni-tui` |
| Go module `ccmon` | `isshoni` |
| `CCMON_ROOT` | `ISSHONI_ROOT` |
| `docs/ccmon.md` | `docs/isshoni.md` |
| dashboard title, window title, error prefixes `ccmon` | `isshoni` |

Unchanged: the `factory` plugin and its agent names, the local folder name, dispatcher session names. No aliases for the old names.

## What the dashboard shows (the human's addition: "rebrand the terminal app itself")
The command stays lowercase (`isshoni`, `isshoni-tui`, error prefixes, `ISSHONI_ROOT` hints). The name on screen is the brand:
- Header: `Isshoni` in the existing gradient, then `一緒に` in the muted colour, from 120 columns. Below that the kanji is left out: it takes seven cells with its space, and it must never push the clock out of the bar. Counted with every part showing (four tabs, `3 live`, `reloading`, the clock): left part 61 cells, right part 53, one between, so 115; 120 leaves room for a longer live count.
- Help overlay: `Isshoni 一緒に  keys`. Loading screen: `I s s h o n i  一緒に`. Too-small screen: `Isshoni`. Window title: `Isshoni`.
- Toasts and the error screens carry no brand name; the fatal screen names the commands.
- Palette and layout are unchanged.
- `tools/ansi2html.py` gives a double-width character exactly two cells, so `make screenshot` lines up whatever font the browser falls back to.
- `TestHeaderBrand` checks, in colour and without, at 60, 80, 100, 119, 120 and 140 columns: the header is exactly as wide as the window, the kanji appears from 120, and there nothing on the right is dropped with every part showing.

## Method
A script kept outside the repository does all of it, and can be run again after main is merged in:
1. `git mv` every tracked path containing `ccmon` to the same path with `isshoni`.
2. In every tracked text file except the two below, replace `ccmon` with `isshoni` and `CCMON` with `ISSHONI`.
3. Delete the README sentence "Isshoni is the project. Its commands and plugin keep their working names ... for now." The README is not otherwise rewritten.
4. `gofmt -w` the Go files and regenerate the golden snapshots.
5. Check: fail if `git grep -i -E 'c ?c ?m ?o ?n'` (the name, or the letter-spaced wordmark the loading screen had) finds anything outside the two files below, or any tracked path contains `ccmon`.

The on-screen wording above is a hand edit on top, which the script does not undo.

## What keeps the old name
- `docs/specs/readme-isshoni.md` (8 lines): the record of the earlier README task, which states what was then true (the commands were `ccmon`, the reference moved to `docs/ccmon.md`, the rename was not done).
- This file.

## Done
- The check passes; `make install`, `make test`, `go test -race ./...`, `make snapshot` and `make screenshot` work.
- `bin/isshoni sessions --json` on the real transcripts matches `bin/ccmon sessions --json` from main on the same frozen copy of real transcripts, except the path of the price file.
- Rendered screenshots (dashboard at 140x40, 100x30 and 80x24, help at 140x40) show the name, the kanji aligned, and the bar full width.
- The golden diff is the title line and the spacing it displaces, nothing else.

## Non-goals
Renaming the factory plugin, the local folder, or anything under `~/.claude`. Old binaries a user built earlier (`bin/ccmon-tui`, git-ignored) are not removed: `rm bin/ccmon-tui`.
