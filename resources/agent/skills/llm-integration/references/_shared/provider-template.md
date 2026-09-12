# Adding a new provider reference

Use this when integrating a provider not yet covered (groq, local vLLM,
etc.). Follow the existing layout exactly so the provider README and shared
matrix stay predictable. Completed examples: `openai/`, `openrouter/`,
`gemini/`, `anthropic/`, `codex/`. Client/runtime trees (not provider HTTP):
`opencode/`, `agent-hooks/`.

## File set (per provider)

```text
references/<provider>/
├── README.md            # router — mandatory once the tree has >3 files
├── chat-completion.md   # or provider-native request/response contract
│                        # (Gemini uses generate-content.md)
├── stream.md            # SSE framing + deviations from OpenAI shape
├── tools.md             # function calling support + flow
├── model-list.md        # discovery endpoint + naming + selection
└── errors.md            # error envelope + retry policy
```

Add modality files only when the provider has a distinct contract
(`images.md`, `tts.md`, `embeddings.md`, `realtime.md`, …). Gemini also
keeps `thinking.md` because thought signatures are load-bearing for agents.

## Required content for contract files

Every request/response contract file must clearly cover the applicable items
below, in this order where the format permits. Router/index files, model
catalogs, stream decoders, and error references may use a specialized layout,
but must still link to the relevant contract details.

1. **Endpoint + auth** — base URL, header, env var name (never a value).
2. **Request contract** — minimal JSON with only the parameters that matter.
3. **Response contract** — exact shape, including where usage lives.
4. **Workflow** — ordered steps from a minimal smoke test to the full feature.
5. **Edge cases** — non-obvious behaviors that change decisions.
6. **Error handling** — provider-specific semantics + pointer to retry policy.

## Rules

- Verify against the provider's live docs and one real smoke test before
  writing — use curl or a raw HTTP client for HTTP endpoints, or the native
  WebSocket/local protocol client when curl is not appropriate. Do not
  transcribe from memory or stale blog posts.
- Record deviations from the OpenAI shape explicitly (field names, error
  envelope, streaming quirks) — that is what the reader is scanning for.
- Link to the shared technique references for compaction, MCP, hooks, and
  reliability instead of duplicating their provider-agnostic rules; add only
  the provider-specific wire differences locally.
- Model lists are snapshots: date them, mark "verify live", and name the
  provider's actual discovery/catalog surface (not an assumed `GET /models`).
- Begin volatile references with their authoritative source URLs and a
  `Verified YYYY-MM-DD` or `Derived YYYY-MM-DD` marker. Label
  unofficial/undocumented surfaces and pin upstream code to a commit where
  possible; a temporary local clone is optional, never a prerequisite.
- Never store API keys, account ids, or request logs with real payloads.
- After writing the files, update the provider README and the relevant
  `references/_shared/provider-matrix.md` row. The SKILL.md table only maps
  broad surfaces to their router; do not duplicate per-provider workflows
  there.
- Add a row to `references/_shared/sample-implementations.md` pointing at the
  best sample code for the new provider (upstream repo path or local sample).
- Run the skill validator and an internal relative-link check after editing.
