# Plugin how-to prompt

Use this as the short text returned by `prompts/get` for `howto`.

- Purpose: what the plugin is for.
- Main tools: name each tool and when to use it.
- Order: explain any required sequence.
- Constraints: root/cwd, credentials, limits, or containment.
- Failure modes: common errors and the safe recovery.
- Schema reminder: use `tool_list`/`tool_search`, then `tool_schema`; do not
  invent arguments.
