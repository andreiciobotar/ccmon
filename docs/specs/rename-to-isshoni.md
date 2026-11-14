# Spec: the monitor is called isshoni everywhere

The project is Isshoni; the monitor's commands were still `ccmon` and `ccmon-tui`, and the README had to explain that. After this task there is one name.

## Facts (counted on main at e2aa114)
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

## Method
A script kept outside the repository does all of it, and can be run again after main is merged in:
1. `git mv` every tracked path containing `ccmon` to the same path with `isshoni`.
2. In every tracked text file except the two below, replace `ccmon` with `isshoni` and `CCMON` with `ISSHONI`.
3. Delete the README sentence "Isshoni is the project. Its commands and plugin keep their working names ... for now." The README is not otherwise rewritten.
4. `gofmt -w` the Go files and regenerate the golden snapshots.
5. Check: fail if `git grep -i ccmon` finds anything outside the two files below, or any tracked path contains `ccmon`.

## What keeps the old name
- `docs/specs/readme-isshoni.md` (8 lines): the record of the earlier README task, which states what was then true (the commands were `ccmon`, the reference moved to `docs/ccmon.md`, the rename was not done).
- This file.

## Done
- The check passes; `make install`, `make test`, `go test -race ./...`, `make snapshot` and `make screenshot` work.
- `bin/isshoni sessions --json` on the real transcripts is byte-identical to `bin/ccmon sessions --json` from main (same `--projects-dir`, taken back to back).
- A rendered dashboard screenshot shows `isshoni` in the title bar; the 80x24 goldens show the bar still fits.
- The golden diff is the title and the spacing it displaces, nothing else.

## Non-goals
Renaming the factory plugin, the local folder, or anything under `~/.claude`. Old binaries a user built earlier (`bin/ccmon-tui`, git-ignored) are not removed: `rm bin/ccmon-tui`.
