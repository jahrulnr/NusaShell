# Domain layer instructions

Applies to `domain/` in addition to the repository root `AGENTS.md`.

## Boundary

This layer owns entities, value objects, state transitions, validation,
policies, and deterministic decisions. It must not depend on application,
contracts, infrastructure, transport, UI, or provider SDK code. Do not expand
legacy I/O or external-library coupling in this layer; move effects behind an
application port when changing that behavior.

Keep wire names, JSON compatibility policy, storage layout, HTTP concerns,
and framework types outside the domain. Domain errors and decisions should be
expressible without starting a server, opening a file, or calling a provider.

## Reuse before creation

1. Search the whole `domain/` package for the entity, state, validation rule,
   ID helper, or decision being changed. Read its methods and `_test.go` file.
2. Extend the entity that owns the invariant. Do not create a second model of
   the same concept for one caller or move orchestration into a domain type.
3. Reuse existing constructors, enums, transition methods, validation
   functions, and policy helpers. Preserve invariants such as formed message
   identity, append-only histories, normalized events, and explicit states.
4. Add a new type only when it names a distinct domain concept used by the
   current behavior. Do not add interfaces here for infrastructure concerns.
5. If two policies merely look alike, keep them separate until their semantic
   contract is demonstrably the same. Avoid generic containers and option
   structs created only for hypothetical variants.

Useful starting points include `conversation.go`, `provider.go`, `event.go`,
`workflow.go`, `dag.go`, `todo.go`, `plugin.go`, and `project_memory.go`.
Search by behavior first because the correct owner may not match the feature
name.

## Change shape

- Put deterministic decisions in small functions or entity methods, with
  effects and orchestration in `application/`.
- Make invalid states hard to represent when this does not complicate callers.
- Preserve backward-compatible decoding deliberately. Do not silently change
  persisted semantics or public enum values.
- Use the repository clock wrapper where a domain timestamp must follow the
  machine-local timestamp policy. Inject time for policies that need
  deterministic tests.

## Verification

Write or extend the nearest table-driven test before implementation. Cover
valid, invalid, boundary, and transition cases. Then run:

```text
go test ./domain/...
go test -race ./domain/...
```

If a domain change affects a wire shape or persisted format, also follow
`contracts/AGENTS.md` and test the owning adapter.
