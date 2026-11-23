# Development guidelines

## General approach
- Work incrementally, implementing requests as they arise.
- Prefer simple solutions over elaborate architecture.
- KISS. DRY. YAGNI. Red/Green/Refactor TDD where practical: write a failing test, make it pass with minimal code, then clean up while tests stay green. Where a test is impractical, state how the change was verified.
- Extract shared code on the third repetition, not before.
- Optimize only for a measured need. Handle errors explicitly at I/O and input boundaries.
- Inspect existing code before making changes.
- Keep changes minimal; do not refactor unrelated code.
- Avoid unnecessary abstractions, dependencies and files.
- Do not introduce architectural patterns without a concrete need.
- Ask for clarification when a requirement is genuinely ambiguous.

## Security
- Never commit secrets, credentials or `.env` files.
- Do not add a dependency without stating why; pin its version.

## Project management

- README.md is the canonical project description.
- TODO.md is the canonical project status and task list.
- Do not create additional planning or documentation files without explicit justification.
- Keep TODO.md updated when completing tasks.
- Do not mark work complete until tests and required manual verification have passed.
- Record important architectural decisions in the Decisions section of TODO.md, with a one-line rationale.

## Inbox
- `inbox/` holds the user's notes (changes, additions, bugs), named `YYYYMMDD_HHMMSS.md`. Processed notes are renamed `done_YYYYMMDD_HHMMSS.md`.
- When asked to process the inbox, first read all pending notes and ask the user, in one go, about anything unclear; wait for the answers, then implement every note as clarified.
- Then act on pending notes oldest first, on one branch for the whole run: implement each change or fix following the guidelines above, one commit per item.
- Commit each item's code and its TODO.md update together, renaming the note to `done_` in the commit of its last item. Never commit TODO.md or note renames on their own, and never edit a note's content.

## Planning
- Do not create implementation plans or separate planning documents for routine tasks.
- For larger changes, briefly explain the proposed approach before implementation.
- Do not create additional agents or delegate work unless explicitly requested.

## Documentation
- Keep documentation minimal and accurate.
- README.md describes the software, how to use it, and the exact commands to build, test and lint it (Development section). Keep those commands current.
- TODO.md is the source of truth for project status, outstanding work, known issues and decisions.
- Do not create additional documentation files without a concrete reason.
- Never duplicate source code or implementation details in documentation.
- Update documentation only when relevant information changes.

## Verification
- Run relevant tests and lint after making changes, using the commands in README.md.
- Never mark unverified functionality as complete.
- Report tests that could not be run.

## Completion report
After each task, briefly report:
1. What changed.
2. What works now.
3. Tests performed and their results.
4. Remaining issues or important decisions.

## Git workflow
- Commit and push after completing the originally requested scope, once tests and lint pass.
- If manual verification is still pending, commit and push the branch anyway, keep the task open in TODO.md and say so in the completion report.
- Review changes with git diff.
- Use concise, descriptive commit messages.
- Do not include unrelated changes in a commit.
- Treat new requirements discovered during manual testing as separate tasks and commits.
- Do not amend previous commits unless explicitly requested.
- Never force-push, `reset --hard`, or delete files or branches without asking first.
