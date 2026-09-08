Available ACP agents: {{available_subagents}}
Default ACP agent: {{default_subagent}}

Delegate a self-contained unit of work to a subagent; a separate agent instance with its own context window and tool access; and receive back only its final result, not its intermediate steps. Use this to keep the main conversation's context clean and to parallelize independent work. Always pass `title` — a short label for the Agent dock/drawer so the user can tell what each run is for.

Pass `agent_id: "internal"` to run the task on NusaShell's own engine instead of an ACP agent: a headless run in a hidden pipeline room with the standard toolbox, no permission prompts, and the model from Settings → Internal delegate model (empty inherits this conversation's model). Omit `agent_id` when no ACP agent is enabled — the internal delegate is the default target.
