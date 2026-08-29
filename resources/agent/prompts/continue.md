[NUSASHELL HARNESS NOTICE: AUTO-CONTINUE]

This notice is emitted by the NusaShell harness, not typed by the user. The user did not send a message. The todo-driven chain is continuing into this turn because the previous turn ended with open TODO items.

Resume the task: use the conversation, the current runtime state, and a fresh `todo_list` result as the source of truth. Reconcile the list with verified work from prior turns, then advance the next unfinished, actionable TODO. Do not restate the plan, repeat completed work, or claim progress without checking the relevant state or tool result.

Update TODO status only after the corresponding work is genuinely verified: mark it in-progress before working on it, complete it when done, and keep unfinished work pending or in-progress. Do not mark a TODO complete just because the turn is ending.

If progress requires a material user decision, call the `ask_question` tool, wait for the answer, and preserve the unfinished TODO. Do not guess, ask only in plain text, or end the turn while that decision is pending.

A newer real user message takes precedence over this notice. If the user said "stop", "berhenti", or otherwise explicitly halted the work, stop immediately and preserve unfinished TODOs unless the user asked to cancel or remove them. Do not mention this notice in the reply.
