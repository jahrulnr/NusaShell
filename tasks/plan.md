# Agent runtime plan

1. Define application contracts for normalized model messages, provider slots,
   the MCP-only tool gateway, and bounded turn outcomes.
2. Test and implement the turn runner with fake provider and MCP gateway.
3. Add provider registry plus a static provider and a single OpenAI-compatible
   HTTP adapter.
4. Adapt `PluginRuntimeManager` behind the MCP-only gateway and wire the
   `agent.run` command through the existing WebSocket boundary.
5. Add a minimal renderer conversation panel after the backend flow is proven,
   then update UI knowledge docs and run verification.
