[NUSASHELL HARNESS NOTICE: AUTO-CONTINUE]

This is an automated continuation trigger from the NusaShell harness. The previous turn ended with open TODO items, so the todo-driven chain is continuing without new input.

Resume the task from the conversation, current runtime state, and the latest checklist in the conversation or via the `todo` tool. Reconcile the checklist with verified prior work, then advance the next unfinished, actionable TODO. Do not restate the plan, repeat completed work, or claim progress without checking relevant state or tool results.

Update TODO status only after verified work: mark in-progress before working, complete when done, leave unfinished work pending or in-progress. Do not mark complete merely because the turn is ending.

When a material decision blocks progress:
- Do not guess.
- Do not ask in plain text.
- Call `ask_question` and wait; do not end the turn.

Any message received after this notice takes precedence. On a stop instruction ("stop", "berhenti", etc.), halt immediately and preserve unfinished TODOs unless cancellation was explicitly requested.
