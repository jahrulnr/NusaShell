# Provider capability contract

Use this as a decision model before writing a provider adapter. It is not a
replacement wire format: the selected provider reference remains authoritative.
The purpose is to make capability assumptions explicit and prevent
"OpenAI-compatible" from being mistaken for feature-compatible.

## Capability record

Represent the selected provider/model/endpoint with a record like this:

```text
capability = {
  provider,
  model,
  endpoint,
  transport,             # http | sse | websocket | multipart | event-stream
  auth_mode,
  input_modalities,      # text | image | audio | video | file
  output_modalities,     # text | json | image | audio | video | embedding
  streaming,             # supported | unsupported | conditional | unknown
  tools,                 # same status enum
  structured_output,
  reasoning,
  prompt_caching,
  compaction,
  mcp_bridge,
  realtime,
  async_jobs,
  usage,
  limits,                # context, output, request bytes, tool count, etc.
  discovery_surface,
  retention_mode
}
```

Use the status values `supported`, `unsupported`, `conditional`, and `unknown`
for features. Treat `unknown` as disabled until verified; do not silently
fallback to a weaker behavior when the product depends on the feature.

## Selection flow

1. Extract requirements from the task: modalities, latency, tools, reasoning,
   context length, retention, cost, regional constraints, and async behavior.
2. Select a provider and endpoint family. A model name alone is not enough;
   the same model family can expose different contracts across platforms.
3. Read the provider's model/catalog and contract references. Use the actual
   discovery surface for that provider; never assume it is `GET /models`.
4. Build the capability record and mark every load-bearing feature. Verify
   conditional features with a minimal request or documented capability field.
5. Choose the narrowest API surface that satisfies the requirements. Keep
   provider-specific request/response mapping inside one adapter boundary.
6. If fallback is needed, compare the fallback's capability record before
   sending the request. A fallback that cannot preserve tools, schema, or
   reasoning state is a product behavior change, not a transparent retry.

## Load-bearing dimensions

| Dimension | Questions to answer before implementation |
|---|---|
| Transport | Is the response REST, SSE, WebSocket, multipart, NDJSON, or binary event-stream? What is the terminal/cancel signal? |
| Auth | Which credential type and headers are valid? Is the credential scoped to a project, workspace, region, server, or user? |
| Input | Which content parts and file lifecycle are accepted? Are URLs, data URLs, or uploads allowed? |
| Output | Is the result text, structured JSON, raw bytes, an embedding vector, or an async job? |
| Tools | Are tools supported, strict, parallel, server-side, or provider-specific? How are call ids and errors mapped? |
| Reasoning | Are thinking blocks/signatures/encrypted items returned? Must they be replayed, preserved, or omitted? |
| Context | Is state stored by the provider, replayed by the client, or compacted through a dedicated API? |
| Limits | What are context/output/request-size/tool-count/rate/retention limits? |
| Usage | Where do token, media, cache, latency, and cost fields appear, if at all? |
| Fallback | Can the alternate provider preserve the same user-visible and side-effect semantics? |

## Adapter boundary

Expose only behavior the selected endpoint can prove:

```text
call(request, capability) -> response | typed_error
stream(request, capability) -> events | typed_error
parse(response_or_events, capability) -> normalized_result
invoke_tool(call, capability) -> validated_tool_result
compact(state, capability) -> checkpoint | unsupported
```

The normalized layer may expose common concepts, but it must retain the raw
provider data needed for debugging and replay. Do not erase distinctions such
as `tool_use` vs `function_call`, `[DONE]` vs `response.completed`, or
`thoughtSignature` vs encrypted reasoning.

## Common false assumptions

- OpenAI-compatible request shape does not guarantee tools, reasoning fields,
  structured output, embeddings, media, or identical error semantics.
- A model catalog entry does not necessarily describe account, region,
  deployment, plan, or provider-route availability.
- `stream: true` does not imply SSE; Bedrock event-stream, Ollama NDJSON, and
  WebSocket protocols need different parsers.
- A fallback model may not accept the original tool schema or preserve opaque
  reasoning/compaction state. Rebuild or reject explicitly.
- Capability discovery is not authorization. Still validate auth scope,
  tenant policy, and tool permissions at execution time.

## Verification checklist

- [ ] Every load-bearing feature has a documented or tested capability status
- [ ] Endpoint, transport, auth, and discovery surface are recorded
- [ ] Limits include an output reserve and request-size/tool budget
- [ ] Fallback candidates are checked for semantic compatibility
- [ ] Raw provider fields needed for replay/debugging are retained
- [ ] Unknown capability never silently enables a feature

Related routing: [provider-matrix.md](provider-matrix.md),
[context-management.md](context-management.md), [mcp.md](mcp.md), and the
selected provider's `model-list.md`, `tools.md`, and `stream.md`.
