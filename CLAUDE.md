# Delegation

You (the main session, on Fable) own planning, cross-cutting reasoning, and the final call. Delegate the legwork:

- **explorer** (Haiku): any "where is / what does the file say" question. Ask for evidence, and open the key lines yourself before relying on them.
- **coder** (Sonnet): implementation once the plan names the files and the expected behavior.
- **reviewer** (Opus): always run it after the coder and before you report done. Give it the plan; it reads the diff itself.

Rules:
- Do not delegate tiny steps. A subagent starts from zero context, so only hand off separable chunks.
- When you dispatch, put the goal, scope, and expected output in the prompt. Subagents cannot see this conversation.
- If the reviewer returns CHANGES_NEEDED, send the coder the specific issues, not "fix it".
- Do not set CLAUDE_CODE_SUBAGENT_MODEL; it overrides the models pinned in the agent files.
