# ccmon

Delegation monitor for Claude Code: a Fable orchestrator with pinned subagents
(explorer on Haiku, coder on Sonnet, reviewer on Opus), and a CLI that reports
tokens, dispatches and re-dispatches from session transcripts.

- `.claude/agents/` - the three subagents
- `CLAUDE.md` - delegation rules for the main session
- `docs/MONITOR_BRIEF.md` - build brief for `ccmon`

## Start
```
claude --model fable
```
Enter plan mode and say: "Read docs/MONITOR_BRIEF.md. Plan it, then build it
using the explorer, coder and reviewer agents."
