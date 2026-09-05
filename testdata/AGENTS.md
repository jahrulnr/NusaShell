# Testdata instructions

Applies to `testdata/` in addition to the repository root `AGENTS.md`.

## Purpose

This directory contains stable cross-boundary fixtures and executable fakes
used by production-package tests. It is evidence of a contract, not an
alternate implementation of product behavior. Package-local fixtures stay
beside the package when they are not shared across boundaries.

## Reuse before creation

- Search `testdata/fakemcp`, `testdata/fakeacp`, `testdata/golden`,
  `contracts/testdata/golden`, `infrastructure/testdata`, and existing test
  harnesses before adding a fixture server or payload.
- Extend the existing fake protocol peer with a deterministic mode when it
  already models the same boundary. Do not create one fake executable per
  test case.
- Reuse canonical IDs, timestamps, envelopes, and fixture builders from the
  owning tests. Avoid near-duplicate JSON samples that differ only in
  irrelevant formatting.
- Add a shared fixture only when more than one package or a true process
  boundary needs it. Otherwise keep the data local to the test.

## Fixture rules

- Keep inputs deterministic, minimal, human-readable, and free of production
  credentials or personal data.
- Golden files pin intentional bytes. Review field names, ordering, omission,
  enum values, and line endings before accepting an update.
- Fakes model the external protocol, including failure, cancellation, partial
  frame, ordering, and shutdown behavior required by the test. They must not
  duplicate the production algorithm under test.
- Do not make tests depend on public network access, wall-clock timing, the
  user's home directory, or mutable machine state.
- Preserve stable paths expected by handler-level tests and cross-platform
  execution. Use forward-slash logical paths in fixture content when the
  protocol is platform-neutral.

## Verification

Run the owning package tests for every fixture change. For shared MCP/ACP or
wire fixtures, this normally includes:

```text
go test ./contracts/...
go test ./infrastructure/...
go test ./transport/...
```

Use narrower package commands while iterating. Inspect golden diffs manually;
a regenerated fixture is not automatically a correct fixture.
