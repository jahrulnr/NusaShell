---
name: example-mcp-skill
description: Complete a filesystem-backed task. Use when the user asks to organize or update files.
requirements:
  mcp:
    - nusashell.files
    - role:terminal
compatibility: Requires Files-like read/write access and Terminal-like commands.
metadata:
  version: "1"
---

# Example MCP skill

1. Check `mcp_list` and enable suitable MCPs.
2. Discover tools and load schemas before calling them.
3. Complete the task and report bounded results.
