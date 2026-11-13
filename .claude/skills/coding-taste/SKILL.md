---
name: coding-taste
description: Optional coding workflow, used only when explicitly requested. Emphasizes minimum total complexity, delegation, real validation, precise communication, and independent review until LGTM.
---

# Coding Taste

Deliver the complete requested outcome with the least total system complexity.

## Standard

Simplicity means the smallest complete design, not fewer lines or reduced scope. Never omit required behavior, integration, validation, security, migration, or cleanup.

Choose the first complete option: **delete → reuse existing owner/helper → change config/data → add one direct path → extract one small deduplicating helper → add an abstraction only for current callers when it removes more complexity than it adds**. Stop there. Keep one path, one owner, and one source of truth.

Reject speculative compatibility, modes, flags, extension points, frameworks, duplicate implementations, forwarding wrappers, one-use generic code, no-op scaffolding, premature file splitting, misplaced special cases, and comments that	 narrate visible code.

## Execute

1. **Define done:** exact outcome, interfaces, invariants, security boundaries, tests, and non-goals. Do not expand or reduce scope.
2. **Inspect first:** read repository instructions; find the real owner, runtime path, existing helpers, conventions, and narrowest real test. Preserve unrelated changes and use the project's environment.
3. **Orchestrate early:**
   - Delegate independent implementation, investigation, validation, feedback, and review.
   - Root owns priorities, steering, integration, and final checks; write directly only for small or cross-cutting work.
   - Give each writer an exclusive worktree/path. Keep reviewers read-only in clean worktrees.
   - While work runs, do the next non-conflicting task: CI, PR feedback, traces, tests, or review. Never wait or repeatedly poll.
   - Observe only to continue, steer, cancel, or integrate. Mark every result accepted, deferred, or rejected.
4. **Implement end to end:** connect the real public path. Add only required code, errors, security, tests, config, and docs.
5. **Validate:** run focused, integration, end-to-end, and repository checks. Fix and rerun. For PRs, inspect current SHA, required CI, mergeability, reviews, comments, and unresolved threads; address valid feedback and recheck after every push.
6. **Simplify:** remove duplication, nesting, indirection, flags, wrappers, files, and comments until further deletion removes required behavior or clarity.
7. **Review until LGTM:** give an independent reviewer the request, repository rules, exact diff, and checks. Fix valid findings, rerun checks, and repeat.

Do not commit, push, publish, or change external state without authorization.

## Communicate

Assume every token costs $1. Lead with the result. Use short, concrete statements with exact names, paths, commands, SHAs, blockers, owners, and next actions. Remove filler, repetition, vague claims, and routine narration. State uncertainty directly.

## Done

Finish only when behavior works, delegated work is settled, the diff is clean, checks pass, and the current diff has independent LGTM. For PRs: current SHA pushed, required CI green, valid comments addressed, threads resolved, and mergeability verified.

Report only the result, main simplification, checks, review verdict, blockers, and anything unverified.