# Spec: plugin-namespaced agent types count as their role

A plugin's agents are dispatched as `<plugin>:<name>` (`factory:coder`). The monitor compared agent types with the bare names, so factory tasks showed no review loops, no re-checks, and hash colours in the dashboard. A namespaced type now counts as its role wherever the role matters.

## Facts this depends on (full counts over `~/.claude/projects`, 2026-11-14, 1,438 files)
- `subagent_type` on `Agent` tool calls, unique tool-use ids: `factory:reviewer` 6, `factory:explorer` 5, `factory:coder` 3; 630 bare. `agentType` in the 665 `agent-*.meta.json` files: the same three, 6, 5 and 3. (The brief's 9/10/6 counted re-logged duplicate records.)
- No other namespaced type occurs, none has more than one colon, and no bare type contains a colon.
- Nothing in a transcript or meta file records where a plugin agent's definition was loaded from. The meta keys are `agentType`, `description`, `toolUseId`, `spawnDepth`, `requestShape`, ... (no path). The factory plugin is loaded with `--plugin-dir` and is absent from `~/.claude/plugins/installed_plugins.json` and the plugin cache. The plugin directory appears only in free text and in one launch argv of an unrelated session.

## Rule
The role of an agent type is the text after its last `:`; a bare type is its own role. `factory:coder` → `coder`, `coder` → `coder`.

The rule lives in two places, each one line: `role()` in `ccmon/metrics.py` and `agentRole()` in `internal/tui/theme.go`. The JSON does not carry the role: `agent_type` and the `by_agent` keys keep the full name, and the dashboard derives colour and order from it.

## Python (`ccmon/metrics.py`)
- Review loops: a verdict comes from a dispatch whose role is `reviewer`; a dispatch or resume answers it when its role is `coder`.
- Re-check: a candidate is a dispatch whose role is in `EXPLORER_TYPES`.
- Repeats: dispatches are compared within one role, so `coder` and `factory:coder` prompts are compared with each other.
- Model check: the expected model of a namespaced type cannot be found reliably (see Facts), so:
  - by default (`<cwd>/.claude/agents`, `~/.claude/agents`) a namespaced type is looked up under its full name, which finds nothing: no check, no warning. It is never compared with a bare `.claude/agents/<role>.md`, which is a different definition.
  - with `--agents-dir DIR` the user has said where the definitions are, and a namespaced type is looked up there by its role (`DIR/coder.md` for `factory:coder`). `ccmon --agents-dir factory/agents ...` therefore checks factory agents. The warning text names the full type.

## Display (decided)
- The full name is shown everywhere (`factory:coder`), never shortened: it says which definition ran, and a plugin's `coder` may pin a different model than the repository's.
- Per-agent totals (`agents` view and `by_agent`) keep `coder` and `factory:coder` as two rows, for the same reason: they are two definitions, and a p50 over both describes neither. CLI ordering is unchanged (alphabetical after `main`).
- Dashboard: `AgentColor` gives a namespaced type the colour of its role; an unknown role keeps a hash colour of the full name. `agentLess` ranks by the role's position in `agentOrder`, unknown roles after them alphabetically by role; within one role the bare type comes first, then the namespaced ones by full name. The order is `main, coder, factory:coder, reviewer, factory:reviewer, ...`. Timeline lanes stay one per full name.

## Known limits
- A plugin agent literally named `<plugin>:main` gets main's colour and sorts next to it; its numbers stay separate.
- A namespaced agent's model is not checked unless `--agents-dir` is given.

## Tests
- Python: a fixture session with `factory:reviewer` (CHANGES_NEEDED) answered by `factory:coder`, a `factory:explorer` with a citation that the main session then reads, and two similar `factory:coder` prompts: review loops, re-check and repeat are counted; with the fixture agents dir a wrong-model `factory:*` dispatch warns; `expected_model("factory:coder", cwd)` without an agents dir is `None` even when `<cwd>/.claude/agents/coder.md` exists. Existing tests pass unchanged.
- Go: `AgentColor("factory:coder")` equals `AgentColor("coder")`; `agentLess` order with mixed bare and namespaced names.
- Real data: `ccmon sessions --json` and `agents --json` before and after differ only in sessions that dispatched `factory:*` agents; one of the three factory leads is checked by hand against its transcript.
