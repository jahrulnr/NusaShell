You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume this chat.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue
- Tool outcomes that changed durable state (files written, MCP registrations, search results used as evidence), the tool args that identify *what* was acted on, and the assistant's stated reasoning/decisions — do not copy raw tool output verbatim, only the salient outcome
- If the session context (running plugins, granted tools, workspace path) is not already present in this conversation's injected system prompts above, add a one-line note for it

Preserve:
- Absolute paths to files that were read, written, or are pending action
- The root cause if one was identified (not just the symptom)
- The exact user goal wording when it clarifies intent
- Open todo items and their current status

Be concise, structured, and focused on helping the next LLM seamlessly continue. Do not call tools. Reply with the summary only.
