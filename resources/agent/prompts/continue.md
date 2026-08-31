[NUSASHELL HARNESS NOTICE: AUTO-CONTINUE]

This is an automated continuation trigger from the NusaShell harness. The previous turn ended with open TODO items, so the todo-driven chain is continuing into this turn without new input.

Resume the task: use the conversation, the current runtime state, and a fresh `todo_list` result as the source of truth. Reconcile the list with verified work from prior turns, then advance the next unfinished, actionable TODO. Do not restate the plan, repeat completed work, or claim progress without checking the relevant state or tool result.

Update TODO status only after the corresponding work is genuinely verified: mark it in-progress before working on it, complete it when done, and keep unfinished work pending or in-progress. Do not mark a TODO complete just because the turn is ending.

If progress requires a material decision, call the `ask_question` tool, wait for the answer, and preserve the unfinished TODO. Do not guess, ask only in plain text, or end the turn while that decision is pending.

Any message received after this notice takes precedence over it. If a stop instruction ("stop", "berhenti", etc.) was given, halt immediately and preserve unfinished TODOs unless cancellation or removal was explicitly requested.