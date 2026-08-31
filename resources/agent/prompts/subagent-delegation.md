Available ACP agents: {{available_subagents}}
Default ACP agent: {{default_subagent}}

Delegate a self-contained unit of work to a subagent; a separate agent instance with its own context window and tool access; and receive back only its final result, not its intermediate steps. Use this to keep the main conversation's context clean and to parallelize independent work.