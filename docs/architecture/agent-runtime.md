# Agent runtime

## Objective

NusaShell can run a bounded AI turn that uses only MCP tools exposed by
currently running plugins. The shell remains the broker: the model never gets
an MCP transport, process handle, or plugin UI channel.

## Scope

- A provider registry normalizes named provider slots without leaking API keys.
- A deterministic turn loop asks a provider for text or tool calls, invokes
  namespaced MCP tools through `PluginRuntimeManager`, and feeds results back
  into the next round.
- A turn has a generated trace ID and structured lifecycle logs.
- The first real adapter uses the OpenAI-compatible Chat Completions dialect;
  a static provider supports repeatable local tests and offline development.

## Non-goals

- Conversation persistence, streaming, cancellation, retries, model picker,
  and provider settings UI are intentionally deferred.
- The agent does not receive CRUD, filesystem, shell, document, or hidden
  shell tools. Future `docs_*` tools must be MCP tools like every other tool.
- No direct plugin-to-provider or plugin-to-plugin connection is added.

## Flow

```text
agent.run (WebSocket command)
  -> RunAgentTurnHandler -> InProcessAgentTurnWorker
  -> AgentTurnRunner
     -> ProviderRegistry -> selected AI provider
     -> McpToolGateway -> PluginRuntimeManager -> MCP client
  -> response + structured trace logs
```

Tool names are stable and collision-safe: `<pluginId>.<toolName>`. The gateway
builds this allowlist from `PluginRuntimeManager.listTools` and rejects a model
call that is not in it. Tool failures become a bounded result for the next
model round; they do not crash the process or bypass the broker.

## Configuration

Environment is the initial configuration boundary:

| Variable | Purpose |
| --- | --- |
| `NUSASHELL_AI_PROVIDER` | Provider slot ID, default `stub` |
| `NUSASHELL_AI_MODEL` | Model for the OpenAI-compatible provider |
| `NUSASHELL_AI_BASE_URL` | API base URL, without a trailing slash |
| `NUSASHELL_AI_API_KEY` | API key; never returned or logged |
| `NUSASHELL_AI_MAX_TOOL_ROUNDS` | Maximum model/tool rounds, default `8` |

When the required real-provider values are absent, `stub` remains usable. A
provider slot must be selected explicitly before any network request is made.

## Acceptance criteria

1. A text-only provider result completes in one round.
2. A requested allowlisted MCP tool executes through the runtime manager and
   its result is included in the next model request.
3. Unknown, malformed, or non-running tools are rejected without execution.
4. The loop stops at the configured round limit.
5. Trace logs identify each turn, provider request, tool execution, and result
   without logging API keys or raw prompt/tool arguments.

## Verification

Run focused application tests during implementation:

```bash
pnpm --filter @nusashell/application test -- agent-turn
```

Then run all application tests and the workspace build/type checks that are
not already failing outside this slice:

```bash
pnpm --filter @nusashell/application test
pnpm build
pnpm typecheck
```
