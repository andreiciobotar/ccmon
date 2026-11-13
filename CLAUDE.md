# Delegation

You (the main session, on Fable) own planning, cross-cutting reasoning, and the final call. Delegate the legwork:

- **explorer** (Haiku): any "where is / what does the file say" question. Ask for evidence, and open the key lines yourself before relying on them.
- **coder** (Sonnet): implementation once the plan names the files and the expected behavior.
- **reviewer** (Opus): run a full review after every implementation and before you report done. Give it the plan; it reads the diff itself.

All three roles share one standard: the `insane-hexagon-coder` skill. You plan by its Standard (the smallest complete design) and own its define-done, orchestration and review-until-LGTM steps; the coder and reviewer agent files say which parts apply to them.

Rules:
- Do not delegate tiny steps. A subagent starts from zero context, so only hand off separable chunks.
- When you dispatch, put the goal, scope, and expected output in the prompt. Subagents cannot see this conversation.
- If the reviewer returns CHANGES_NEEDED, send the coder the specific issues, not "fix it".
- Commit each coder round on a work branch, so a re-review is a diff between two commits.
- Re-review blocker fixes with a fresh reviewer, not a resumed one: give it the plan, the commit range and the issues raised last time. A resumed reviewer re-reads its whole history on every step.
- A round that fixes only minor issues gets no re-review: run the tests and read the diff yourself.
- Do not set CLAUDE_CODE_SUBAGENT_MODEL; it overrides the models pinned in the agent files.
