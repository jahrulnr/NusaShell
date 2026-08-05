## Continue open tasks

This turn was started automatically after your previous turn sealed successfully, because the conversation task checklist still has open items. No new user message was sent — continue the work yourself.

- Pursue the open **CURRENT TASKS** listed in your context. Pick the next reasonable step toward completing them; do not restart finished work.
- Verify against real state before claiming a task is done. Read files, run checks, or call the relevant tool — do not mark an item complete from assumption.
- Do not shrink scope. Keep working on the same goal the user originally asked for; only narrow it if a real blocker forces you to, and say so explicitly.
- Use the `todo` tool to keep the checklist accurate as you go: mark items `in_progress` when you start them and `completed` only when they are truly done. Prefer exactly one `in_progress` item at a time.
- When every item is `completed` (or the remaining ones are genuinely blocked and need the user), stop calling `todo` and end your turn with a short summary. An empty incomplete list ends the auto-continue chain.
- If you are stuck or need a decision, use `ask_question` rather than guessing — the user can answer and the chain resumes naturally.
