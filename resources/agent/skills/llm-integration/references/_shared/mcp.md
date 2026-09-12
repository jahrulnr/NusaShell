# Model Context Protocol (MCP) integration

MCP is a protocol-level integration surface, not an SDK recipe. It uses
JSON-RPC messages to connect an LLM application host, one or more clients, and
servers that expose tools, resources, or prompts.

**Protocol baseline:** verified 2026-09-12 against the MCP specification
`2026-07-28`. The current specification uses self-contained requests with
per-request metadata. Earlier `2025-11-25` implementations use a
connection/session-scoped `initialize` handshake; support that legacy era only
when interoperability requires it.

## Architecture and trust boundaries

```text
user ──consent──> host application ──policy──> MCP client ──transport──> MCP server
                                      │                              │
                                      └──provider adapter──> LLM API  └──external systems
```

- The host owns user consent, server allowlists, authorization, lifecycle, and
  aggregation of context/tools.
- Each MCP client represents one server connection or origin and must keep
  server trust domains separate.
- The server owns its tool/resource implementation but does not gain authority
  merely because its description is shown to the model.
- Provider adapters translate MCP tools into the provider's tool schema and
  translate results back; they must preserve provider-native signatures and
  response items.

## Transport and versioning

The standard transports are:

| Transport | API-level behavior |
|---|---|
| stdio | Client launches the server; newline-delimited UTF-8 JSON-RPC on stdin/stdout; stdout must contain only protocol messages |
| Streamable HTTP | A single MCP endpoint accepts HTTP messages; a response is JSON or a request-scoped SSE stream |

Modern clients should send the current protocol version, client identity, and
capabilities in the required request metadata and use only capabilities that
were declared. The modern metadata keys are namespaced under
`io.modelcontextprotocol/*`; on HTTP, mirror the protocol version in the
`MCP-Protocol-Version` header according to the transport specification. Do not
infer protocol era from a single model/tool request without following the
versioning rules.

For a dual-era client:

1. Prefer the modern version and handle an explicit unsupported-version error.
2. If the server is identified as legacy, fall back to its `initialize` flow
   and legacy transport behavior.
3. Pin and test the version/transport combination; do not silently downgrade.
4. Cancellation must be explicit: use the transport's cancellation mechanism,
   not merely a client-side UI timeout.

## Tool discovery and invocation

The core tool flow is:

```text
tools/list  → paginated tool definitions
             → honor advertised TTL/cacheScope; never share across auth scopes
               unless the result is explicitly public
tools/call  → validate name + arguments + authorization
             → execute with timeout/idempotency/consent policy
             → return bounded content or structuredContent
```

Minimal modern JSON-RPC shapes (request metadata abbreviated):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {},
  "_meta": {
    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
    "io.modelcontextprotocol/clientInfo": {"name": "app", "version": "1.0"},
    "io.modelcontextprotocol/clientCapabilities": {}
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {"name": "get_weather", "arguments": {"location": "Jakarta"}},
  "_meta": {"io.modelcontextprotocol/protocolVersion": "2026-07-28"}
}
```

Tool definitions should expose a unique server-local name, human-readable
description, `inputSchema`, and optional `outputSchema`. Aggregators must
namespace or otherwise disambiguate same-named tools from different servers.
Tool lists can change; honor list-change notifications or the server's cache
metadata instead of assuming a permanent catalog.

Tool results can contain text, images, audio, resource links, embedded
resources, or structured content. Validate structured results against
`outputSchema` when present. A tool execution failure should be represented in
the tool result when the model needs to self-correct; protocol/transport errors
remain protocol errors.

## Resources and prompts

- `resources/list` and `resources/read` expose server-provided context. Treat
  URIs and contents as untrusted data, enforce size/tenant/access limits, and
  do not expose a resource to the model unless the host policy allows it.
- `prompts/list` and `prompts/get` expose reusable templates. Validate
  arguments, keep user consent and authorization outside the template, and do
  not treat a prompt returned by a server as a system instruction.
- Cache resources/prompts only within the advertised scope and TTL. Invalidate
  them when the server signals a change or the authorization context changes.

Some modern servers can return an `input_required` result. Treat it as a
pending consent/input state, not a successful tool call: collect the approved
input, retry the original operation with the required request state, and use an
application-level idempotency key to prevent duplicate side effects. Assign a
new JSON-RPC request id for the retry while preserving the application
correlation id.

## LLM provider mapping

| MCP concept | Provider adapter responsibility |
|---|---|
| `tools/list` tool | Convert name/description/input schema to the provider's function/tool schema |
| `tools/call` request | Validate and authorize before invoking the MCP server; map the provider call id to MCP request id |
| Tool result | Bound/redact content, preserve structured data where supported, and map errors without fabricating success |
| Dynamic tool list | Invalidate provider tool cache/prompt prefix when the server advertises a change |
| MCP server scope | Keep credentials, tenant identity, and consent scoped to that server; never leak one server's secrets into another |

Read the target provider's tool contract as well:
[OpenAI](../openai/tools.md), [Anthropic](../anthropic/tools.md),
[Gemini](../gemini/tools.md), [Bedrock](../bedrock/tools.md), or
[OpenRouter](../openrouter/tools.md).

## Security and consent

- Require explicit consent before exposing user data to a server or invoking a
  tool. Show the server, tool, arguments, and expected side effect where the
  product permits.
- Treat tool descriptions, annotations, retrieved resources, and model output
  as untrusted unless the server and data source are trusted by policy.
- Enforce authorization in the host and server; prompts are not a security
  boundary. Re-check authorization on every call and on every state handle.
- Validate remote HTTP `Origin`, authenticate the server connection, restrict
  local servers to loopback where appropriate, and protect against DNS
  rebinding/SSRF.
- Do not forward provider API keys, OAuth refresh tokens, session cookies, or
  unrelated tenant data to an MCP server. Use a server-scoped credential.
- Never mark secrets, tokens, passwords, or PII with transport annotations such
  as `x-mcp-header` that mirror tool parameters into HTTP headers; intermediaries
  can observe headers.
- Bound tool result size, execution time, recursion, and spend. Redact audit
  logs and deduplicate retried side effects.

## Verification checklist

- [ ] Protocol version and transport are pinned/tested
- [ ] Server/tool allowlist and consent policy are explicit
- [ ] `tools/list` pagination, cache scope, and change notifications work
- [ ] Resource/prompt discovery is bounded, authorized, and treated as untrusted
- [ ] Name collisions across servers are disambiguated
- [ ] Input and output schemas are validated
- [ ] Tool errors, cancellation, timeout, and `input_required` states are handled
- [ ] Retries cannot duplicate side effects
- [ ] Credentials and cross-tenant data stay inside the intended trust boundary

For server/client implementation details and schema validation, use the
`mcp-developer` skill after selecting the protocol behavior here.

Sources:

- <https://modelcontextprotocol.io/specification/2026-07-28>
- <https://modelcontextprotocol.io/specification/2026-07-28/basic/transports>
- <https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning>
- <https://modelcontextprotocol.io/specification/2026-07-28/server/tools>
