# NusaShell composition-root instructions

Applies to `cmd/nusashell/` in addition to the repository root `AGENTS.md`.

## Boundary

This directory is the executable composition root. It selects concrete
adapters, loads process configuration, wires `application.Deps`, registers
transport/plugin routes, starts background lifecycles, and performs graceful
shutdown. Feature policy and reusable implementation do not belong here.

## Reuse before creation

1. Trace the requested capability from its application port to an existing
   infrastructure implementation and current wiring in `main.go`.
2. Extend `application.Deps`, the existing store/client/runtime instance, or
   route registration only when the feature requires a new real dependency.
3. Reuse existing environment helpers, data-directory layout, logger, bus,
   tool factory, lifecycle contexts, and shutdown sequence.
4. Keep `main.go` declarative. If code has branches worth unit testing or is
   useful outside startup, move it to the owning inner policy or adapter
   instead of adding another helper cluster here.
5. Do not construct duplicate stores, clients, buses, registries, or runtime
   managers for one feature. Shared process state must retain one owner.

A new command is justified only by a real process-level workflow that cannot
use the running RPC/tool surface. Keep subcommand dispatch small and explicit.
Do not read provider credentials implicitly; preserve the opt-in credential
seeding and loopback security guard.

## Lifecycle checklist

- Every started worker has a cancellation source, an owner, an error path,
  and deterministic shutdown.
- Close resources in reverse dependency order where order matters.
- Keep startup failures actionable and do not continue with half-wired state
  unless the existing contract explicitly permits degradation.
- Preserve platform-specific path and signal behavior.
- Embedded frontend and resources remain wired through their established
  packages; do not add a second asset pipeline.

## Verification

Composition changes need a focused test in this package when helper behavior
changes, plus build and relevant integration coverage:

```text
go test ./cmd/nusashell/...
go build ./cmd/nusashell
go test ./transport/... -run '<relevant test>'
```

Run the process or end-to-end flow when startup, shutdown, route registration,
or runtime wiring changed.
