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
| **Monitor** | Shows where the money and tokens went: per repository, task, session, agent and model | `isshoni`, `isshoni-tui` |
| **Factory** | Builds several tasks in parallel, each in its own git worktree, each ending in a draft pull request | `/factory:dispatch`, from the `factory` plugin |

## Kaizen (改善), built in

Most agent setups run on day 100 the way they ran on day 1. Isshoni's factory learns from every task:

- Every agent reports what slowed it down.
- Every lead records the key calls it made without asking, and why.
- Every finished task leaves a retrospective and, in the monitor, its cost.
- Every site manager alert is marked acted-on or noise, so its precision and recall are measured.

The dispatcher reads it all and proposes edits to the factory's own rules; you decide.

On day one, agents reported the same shell friction eleven times and counting. It became a rule that day ([#8](https://github.com/andreiciobotar/isshoni/pull/8)).

## Monitor

`isshoni` reads the transcripts Claude Code already keeps in `~/.claude/projects` and never writes there. It reports cost and tokens by model and agent, the share spent in subagents, and the signs of wasted work: repeated dispatches, review loops, and agents running on a model other than the one pinned for them.

```sh
git clone https://github.com/andreiciobotar/isshoni && cd isshoni
bin/isshoni sessions --since 7d    # sessions by repository and task, with totals
bin/isshoni session <id-prefix>    # one session: models, agents, dispatch timeline
bin/isshoni agents --since 7d      # per agent type: cost, tokens, p50/p90, repeats
```

Cost is the API list price, from [isshoni/prices.toml](isshoni/prices.toml); edit it when prices change. Python 3.11+, no dependencies. Add `--json` for machine-readable output; [docs/isshoni.md](docs/isshoni.md) has the rest.

`isshoni-tui` is the same data as a live terminal dashboard: sessions grouped by repository and task, a timeline per session, and what is running now. Its factory view (`4`) lists what waits on you across repositories, then each repository's factory tasks with their phase and how many hold a slot against the cap, and `c` there changes the cap: the one thing the dashboard writes (`worktree_cap` in `~/.claude/factory/settings.json`). Its kaizen view (`5`) shows from `isshoni kaizen` whether the factory is getting better: cost, speed, review rounds, defects and the feedback outbox.

```sh
make install    # needs Go 1.27+ and Python 3.11+; builds bin/isshoni-tui
make up         # run it; ? shows the keys
```

<!-- screenshot: isshoni-tui dashboard view -->
<!-- screenshot: isshoni-tui session timeline -->

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

It needs `git`, an authenticated [`gh`](https://cli.github.com) and Python 3.11+. By default leads run unattended on Fable in `auto` permission mode, and five tasks may be in motion per repository. Every session event, lead retrospective and decision a lead took without asking is logged under `~/.claude/factory/` (events for 12 months); `factory feedback` lists the decisions and what agents reported about the factory itself.

A site manager watches the factory for you: every 30 minutes, without running a model, it checks for stuck, blocked or over-limit tasks, red CI and the like, and only when something new turns up starts a short Sonnet run that tells the dispatcher or drops it as noise with a reason. It also merges for you: comment `lgtm` on a handed-over pull request and, checks green and no conflict, the next check merges it. It starts by itself when a session becomes the dispatcher and stops when the last dispatcher session of the repository is gone. It needs `claude` and `gh` on your `PATH` and runs on macOS (launchd) and Linux (a systemd user timer, else cron); Windows is not supported.

<!-- screenshot: factory status with several tasks -->

## More

- [docs/factory.md](docs/factory.md): the factory's commands, states, settings and log formats, and the site manager's criteria
- [docs/isshoni.md](docs/isshoni.md): the monitor's options, the dashboard's keys and how sessions map to tasks
- `make test` runs everything

## Status

Early and changing; built with itself. Source-available, not open source ([MIT with the Commons Clause](LICENSE)): use it, at work too, change it, [contribute](CONTRIBUTING.md). Don't sell it.
