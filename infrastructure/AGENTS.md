# Infrastructure layer instructions

Applies to `infrastructure/` in addition to the repository root `AGENTS.md`.

## Boundary

This layer implements application ports and integrates filesystems,
databases, provider APIs, MCP/ACP processes, installers, generated catalogs,
and built-in tools. Keep business policy in `domain/` or `application/`.
Adapters translate external behavior into existing inner contracts; they do
not redefine those contracts.

## Reuse before creation

1. Find the owning application port and every current implementation before
   designing an adapter. Read sibling adapter tests for error and lifecycle
   conventions.
2. Reuse focused packages already present for JSON and SQLite stores,
   temporary paths, configuration, plugin/skill files, MCP/ACP clients,
   archives/installers, tool output, STT/TTS, and provider translation.
3. Extend an existing adapter when the external system and lifecycle are the
   same. Create a package only for a genuinely separate external boundary,
   not to hold one generic helper or one alternate code path.
4. Use shared path, atomic-write, deep-copy, HTTP, retry, and archive-safety
   helpers where available. Do not fork near-identical persistence or client
   logic.
5. Do not add automatic fallbacks that mask provider, process, or storage
   errors. A fallback requires an explicit product requirement and tests for
   both paths.

For `infrastructure/ai/`, follow the stricter provider-adapter rules in the
root `AGENTS.md`: ported wire packages remain close to upstream and import
`core`; `adapter.go` is the application translation bridge. Search `core/`,
`internal/`, `retry/`, and existing providers before adding wire types or
request options.

## Safety and correctness

- Treat filesystem paths, archives, process arguments, tool input, remote
  responses, and manifests as untrusted at the adapter boundary.
- Keep credentials in the credential store, never JSON, fixtures, logs, or
  generated config.
- Preserve atomic writes, snapshot/deep-copy semantics, cancellation,
  timeout, cleanup, and concurrency safety.
- Inject clients, runners, or filesystem seams only when needed for a
  deterministic test. Avoid interfaces that duplicate application ports.
- Generated files remain generated. Change their source/generator and run the
  documented target instead of patching output by hand.

## Verification

Start with the adapter package and failure-path tests, then run race coverage:

```text
go test ./infrastructure/<package>/...
go test -race ./infrastructure/<package>/...
```

Provider-wire changes need golden/fixture coverage for request and stream
shapes. File/process changes need traversal, cancellation, partial failure,
and cleanup cases where applicable.
