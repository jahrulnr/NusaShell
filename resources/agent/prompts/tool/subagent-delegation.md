Available ACP agents: {{available_subagents}}
Default ACP agent: {{default_subagent}}

Delegate a self-contained unit of work to a subagent (separate context and tools) and receive only its final result. Use this to keep the main conversation clean and to parallelize independent work. Always pass `title` — a short Agent dock label for the run.

Pass `agent_id: "internal"` to run on NusaShell's own engine: a headless run in a hidden pipeline room with the standard toolbox, no permission prompts, and the model from Settings → Internal delegate model (empty inherits this conversation's model). Omit `agent_id` when no ACP agent is enabled — the internal delegate is the default.
