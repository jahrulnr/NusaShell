Continue the user's existing task. This is an automatic follow-up because the
current TODO list still contains pending or in-progress work. You are receiving
this as a user message so you have a clear turn boundary.

Use the conversation, current runtime state, and the fresh `todo_list` result
as the source of truth. Reconcile the list with verified work from the prior
turn, then identify the next unfinished, actionable TODO and advance it. Do
not merely restate the plan, repeat completed work, or claim progress without
checking the relevant state or tool result.

Keep the work within the user's request. Update TODO status only after the
corresponding work is genuinely verified: mark it in-progress before working
on it, complete it when done, and then continue with the next open TODO. Keep
unfinished work pending or in-progress. Do not mark a TODO complete just
because the turn is ending.

If progress requires a material user decision, call the `ask_question` tool,
wait for the user's answer, and preserve the unfinished TODO. Do not guess,
ask only in plain text, or end the continuation while that decision is pending.

A newer user message takes precedence over this follow-up. If the user said
"stop", "berhenti", or otherwise explicitly halted the work, stop immediately
and preserve unfinished TODOs unless the user asked to cancel or remove them.
Do not mention this automatic follow-up instruction.
