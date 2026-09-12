# MCP requirements

Declare requirements.mcp only when a skill really calls an MCP-provided
capability and the target package can preserve optional frontmatter. A
requirement is a routing and availability hint, not an authorization grant.

## Header shape

Use a short list:

    requirements:
      mcp:
        - nusashell.files
        - role:terminal

Use a concrete plugin id when that exact installed provider is required. Use
role:files, role:terminal, or another supported role token when a suitable
provider can satisfy the workflow. Do not list every tool the agent might have.

## Runtime resolution

Before the first MCP call:

1. call mcp_list and inspect running/stopped state;
2. if a required provider is stopped, enable it only when the user-authorized
   workflow needs it;
3. call tool_list or mcp_search on the running provider;
4. call tool_schema for unfamiliar tools or required arguments;
5. call the exact discovered ref through mcp_call;
6. inspect the result and stop or recover according to the skill's contract.

If a requirement is unavailable, explain the gap and use a safe local/read-only
alternative only when it still satisfies the requested outcome. Managed skill
saves cannot set arbitrary frontmatter, so put the operational check in the
body. Do not invent a
server id, tool name, schema, or capability merely to satisfy the header.

## Common mistakes

- Declaring a plugin because it is convenient rather than required.
- Treating requirements.mcp as a permission or automatic enable switch.
- Calling an MCP tool by a guessed mcp__server__tool name.
- Passing a stale ref after a provider restart instead of rediscovering it.
- Putting API keys, OAuth tokens, or private endpoints in frontmatter or prompts.
- Claiming the skill is usable without checking the live provider when the
  workflow depends on it.
