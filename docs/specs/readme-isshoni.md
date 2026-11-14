# The README introduces the project as Isshoni

## Done means
- `README.md` opens with the logo, the name Isshoni, 一緒に and its meaning ("together"), and one sentence saying what the project is.
- It then says briefly what the monitor (`ccmon`, `ccmon-tui`) and the factory (the `factory` plugin) do, and how to try each in a few commands.
- It is honest that Isshoni is the project name while the repository, commands and plugin keep their names.
- No claim about anything that is not on `main`. No local paths, no personal names beyond the public repository URL.
- Detail lives in `docs/specs/` and `--help`; the README links there.
- The logo is in `assets/` in a light and a dark variant, switched by `<picture>` and `prefers-color-scheme`.
- Screenshot places are marked with HTML comments (invisible, no broken links); screenshots come later.
- There is no LICENSE; the README says so and invents none.

- The ccmon reference the README used to carry (options, exit codes, timeline flags, grouping, plugin agents, resumed agents) moves unchanged to `docs/ccmon.md`, because review found parts of it documented nowhere else.

Non-goals: renaming anything; changing code, the Makefile, other specs or the plugin.

## Facts this rests on
- The name is spelled Isshoni (一緒に). Settled with the human.
- GitHub renders `<picture>` with `prefers-color-scheme` sources, `align="center"` on `<p>` and `<h1>`, tables and HTML comments. Checked by sending the README through GitHub's own renderer (`gh api markdown`): the output keeps all of them and wraps the picture in `themed-picture`.
- Health checks are not on `main` (no match for "health" or "doctor" in `factory/bin/factory`); the outbox is (`factory feedback`). The README mentions the outbox only.
- Subagent models: explorer haiku, coder sonnet, reviewer opus (`factory/agents/*.md`).
- Defaults: cap 5 tasks in motion; Go 1.27 (`go.mod`); Python 3.11 (`Makefile`).
- Every detail cut from the README has another home: the factory's settings, `factory ack`, `feedback set` and exit codes, and the dashboard's `CCMON_ROOT`, `NO_COLOR`, keys and snapshot mode are under `docs/specs/`; the ccmon reference is in `docs/ccmon.md`; the marketplace install is in the README.
- Lead defaults: model `fable`, permission mode `auto` (`docs/specs/factory.md`).
- The repository is `andreiciobotar/isshoni` (renamed during this task; `gh repo view` confirms). No tracked file other than the README named the old address. The CI badge URL resolves there (HTTP 200, SVG).

## Logo
Source: a 1408 px square PNG, navy mark on transparent. Both variants are cropped to the artwork with a 4% margin and scaled to 480 px (shown at 220 px, so sharp on 2x screens). The dark variant inverts HLS lightness per pixel and keeps hue, saturation and alpha: navy becomes pale blue, the black accents white, the pale outlines dark. Shapes are untouched.

## Commands in the README and how each was run
- `bin/ccmon sessions --since 7d`, `session <id-prefix>`, `agents --since 7d`: run against real transcripts.
- `make install`: run. `make up`: needs a terminal; its build step ran and the same binary rendered through `make snapshot`.
- `/plugin marketplace add andreiciobotar/isshoni`, `/plugin install factory@factory`, `/rename`, `/factory:dispatch`, `claude --plugin-dir`: not run here (interactive, and installing changes the user's settings). The syntax is from Claude Code's plugin documentation, `claude plugin validate .` passes on the marketplace file, this task was itself started by a dispatcher through the plugin, and `factory status` was run.

## Follow-up proposal: what a full rename would touch
Not done here. `ccmon` appears on 121 lines in 47 tracked files:
- the Go module name `ccmon` in `go.mod` and every Go import (the GitHub repository is already `andreiciobotar/isshoni`; the README uses that address);
- the Python package `ccmon/` and `python3 -m ccmon`, which the dashboard shells out to;
- the commands `bin/ccmon`, `bin/ccmon-tui`, `cmd/ccmon-tui`, the `CCMON_ROOT` variable, the `Makefile` and `.gitignore`;
- the dashboard's own title and help text, with 11 golden files under `internal/tui/testdata`, and 6 Python test files;
- the plugin and marketplace name `factory` (`.claude-plugin/marketplace.json`, `factory/.claude-plugin/plugin.json`), the agent names `factory:*` that the monitor recognises, the `/factory:dispatch` skill and the `~/.claude/factory/` data directory, which would need a migration;
- seven files under `docs/`.
Open choice for the human: whether the commands become `isshoni` subcommands or keep short names under the new project name.
