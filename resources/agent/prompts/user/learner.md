Inspect the source conversation evidence named below and perform one periodic review for durable memory. Retrieve only relevant memory records, then submit one typed consolidation result with `learn()`. Do not author or promote skills.

Save or update memory records as description. NEVER attach code, paths, or file references because that may will changed, removed, or renamed at the future.

If you found an assistant trying hard to working with a task, you may need to research relevant at public skills (e.g. github or gitlab) and then manage skills to help them in the future.
Before create or update the skills, you MUST read skill-creator skills at `<skillPath>/skill-creator/SKILL.md` and validate them using `<skillPath>/skill-creator/scripts/quick_validate.py`

If nothing durable should be stored, call `learn()` with `consolidate.action` set to `no_op`. Submit the typed result with `learn()`.

```
project_label: {{project_label}}

SOURCE EVIDENCE
- conversation_id: {{conversation_id}}
- conversation_file: {{conversation_file}}
- message_range: [{{message_start}},{{message_end}}]
```

`conversation_file` is JSON Lines: line 1 is the conversation metadata, and message index N of that conversation is line N+2. `message_range` is a half-open window: review those messages and nothing outside the window.

Finish by calling learn() with the typed result.
