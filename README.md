<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/isshoni-dark.png">
    <img src="assets/isshoni-light.png" width="220" alt="Isshoni: a disc with blossoms streaming up to the right">
  </picture>
</p>

<h1 align="center">Isshoni · 一緒に</h1>

<p align="center">
  <i>issho ni</i>, Japanese for "together"
</p>

<p align="center">
  Give Claude Code a brief, get back a reviewed pull request,<br>
  and see where every token went on the way.
</p>

<p align="center">
  <a href="https://github.com/andreiciobotar/isshoni/actions/workflows/test.yml"><img src="https://github.com/andreiciobotar/isshoni/actions/workflows/test.yml/badge.svg" alt="tests"></a>
</p>

Isshoni is two tools for people who run [Claude Code](https://claude.com/claude-code) on their repositories. Each works without the other.

| | What it does | You run |
|---|---|---|
| **Monitor** | Shows where the tokens went: per repository, task, session, agent and model | `ccmon`, `ccmon-tui` |
| **Factory** | Builds several tasks in parallel, each in its own git worktree, each ending in a draft pull request | `/factory:dispatch`, from the `factory` plugin |

Isshoni is the project. Its commands and plugin keep their working names (`ccmon`, `ccmon-tui`, `factory`) for now.

## Monitor

`ccmon` reads the transcripts Claude Code already keeps in `~/.claude/projects` and never writes there. It reports tokens by model and agent, the share spent in subagents, and the signs of wasted work: repeated dispatches, review loops, and agents running on a model other than the one pinned for them.

```sh
git clone https://github.com/andreiciobotar/isshoni && cd isshoni
bin/ccmon sessions --since 7d    # sessions by repository and task, with totals
bin/ccmon session <id-prefix>    # one session: models, agents, dispatch timeline
bin/ccmon agents --since 7d      # per agent type: tokens, p50/p90, repeats
```

Python 3.11+, no dependencies. Add `--json` for machine-readable output; [docs/ccmon.md](docs/ccmon.md) has the rest.

`ccmon-tui` is the same data as a live terminal dashboard: sessions grouped by repository and task, a timeline per session, and what is running now.

```sh
make install    # needs Go 1.27+ and Python 3.11+; builds bin/ccmon-tui
make up         # run it; ? shows the keys
```

<!-- screenshot: ccmon-tui dashboard view -->
<!-- screenshot: ccmon-tui session timeline -->

## Factory

You talk to one session, the dispatcher. For each task it starts a lead: a background session in its own worktree that writes a spec, builds through three subagents (explorer on Haiku, coder on Sonnet, reviewer on Opus), has the result reviewed until it is approved, and opens a draft pull request. A lead asks you only what it cannot decide itself, and nothing merges without you.

In Claude Code, in the repository you want work done in:

```
/plugin marketplace add andreiciobotar/isshoni
/plugin install factory@factory
/rename dispatcher-<repository directory name>
/factory:dispatch
```

Describe a task, then ask for progress whenever you like: the dispatcher answers from `factory status`, which lists every task with its phase and pull request. To try it for one session without installing, start `claude --plugin-dir <clone>/factory`.

It needs `git`, an authenticated [`gh`](https://cli.github.com) and Python 3.11+. By default leads run unattended on Fable in `auto` permission mode, and five tasks may be in motion per repository. Every session event, lead retrospective and decision a lead took without asking is logged under `~/.claude/factory/`; `factory feedback` lists the decisions and what agents reported about the factory itself.

<!-- screenshot: factory status with several tasks -->

## More

- [docs/specs/factory.md](docs/specs/factory.md): the factory's rules, commands, states and settings
- [docs/specs/factory-events.md](docs/specs/factory-events.md): the log formats
- [docs/specs/tui.md](docs/specs/tui.md) and [docs/specs/task-grouping.md](docs/specs/task-grouping.md): the dashboard and how sessions map to tasks
- `make test` runs everything

## Status

Early and changing; built with itself. There is no licence file yet, so this is public but not yet open source.
