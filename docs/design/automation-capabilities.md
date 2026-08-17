# NusaShell Automation Capabilities — Built-in Tools and MCP Providers

Status: Proposed extension to `docs/design/automation-engine.md`

## 1. Explicit capability model

An automation trigger such as:

```yaml
when:
  event: email.received
```

must **not** imply that `email.received` is a hard-coded NusaShell event source.

The event is produced by a **capability provider**. That provider can be either:

1. a NusaShell built-in capability/tool, or
2. an MCP server/tool exposed to NusaShell.

The Automation Engine consumes a normalized capability/event interface and does not need to know whether the provider is built-in or MCP.

```text
                    Automation Engine
                           |
                    Capability Registry
                           |
              +------------+------------+
              |                         |
        Built-in Provider          MCP Provider
              |                         |
        NusaShell code              MCP server
              |                         |
              +------------+------------+
                           |
                    Event / Action
```

This is an important boundary: **provider ownership belongs to the capability layer; scheduling belongs to Automation Engine.**

## 2. Capability types

A capability may provide one or both of these surfaces:

```text
Action capability
    deterministic operation that a workflow can execute

Event capability
    source that can emit events that trigger workflows
```

Examples:

```text
email.read       Action
email.send       Action
email.received   Event

vps.health       Action
vps.health_changed Event
vps.restart      Action

github.issue.create Action
github.push         Event
```

A provider may expose both:

```text
Email provider
  ├── email.read
  ├── email.send
  └── email.received
```

## 3. Built-in versus MCP

Built-in capabilities should be used for NusaShell primitives and capabilities that benefit from zero external dependency.

Examples:

```text
filesystem.*
process.*
shell.*
workflow.*
agent.*
local.git.*
local.notification.*
```

MCP should be used for external integrations or capabilities that are naturally owned by a connector/server.

Examples:

```text
email.*
slack.*
github.*
gitlab.*
notion.*
database.*
cloud.*
```

The exact split is configurable. A capability name should not encode whether its implementation is built-in or MCP.

For example, a workflow should say:

```yaml
- uses: email.read
```

not:

```yaml
- uses: mcp.gmail.read
```

This keeps workflow definitions portable when the implementation changes.

## 4. MCP must be an active runtime dependency for MCP-backed automation

If an automation depends on an MCP capability, the required MCP server must be enabled and available when the workflow needs it.

NusaShell should support an MCP lifecycle policy:

```text
enabled
  -> start automatically when NusaShell needs it

active
  -> currently running and available

off
  -> explicitly disabled by user

missing
  -> configured capability/provider is no longer installed

error
  -> installed/enabled but failed to start or became unavailable
```

For automation purposes, the important state is capability availability, not merely process state.

Recommended effective state:

```text
AVAILABLE
BLOCKED
ERROR
```

## 5. Auto-start MCP on demand

MCP-backed automations should not require every MCP server to run permanently.

Preferred policy:

```text
Workflow becomes runnable
        |
        v
Capability resolver
        |
        v
Is required MCP provider active?
        |
    +---+---+
    |       |
   yes      no
    |       |
    |    auto-start enabled?
    |       |
    |    +--+--+
    |    |     |
    |   yes    no
    |    |     |
    | start   BLOCKED
    |    |
    +----+----+
         |
       execute
```

This gives NusaShell the desired behavior: an MCP server does not have to consume resources continuously, but an enabled automation can transparently start its provider when needed.

## 6. Explicit blocked state

If an MCP dependency is disabled, the workflow must not appear as a generic `failed` workflow.

Example:

```text
VPS monitor

BLOCKED

Reason:
Required capability `vps.health` is provided by MCP server `vps-tools`.
The server is disabled.

[Enable MCP] [Open automation]
```

This is a configuration/dependency state, not an execution failure.

Use a dedicated workflow state:

```text
blocked
```

Recommended run lifecycle:

```text
pending
queued
blocked
waiting
running
success
failed
cancelled
```

A blocked run may become runnable later when its capability provider becomes available.

## 7. Uninstalled MCP behavior

If the user uninstalls an MCP server that an automation depends on, NusaShell should preserve the automation definition but mark it as blocked.

Do not silently delete or rewrite the workflow.

Example:

```text
Automation
  Invoice processor
  Status: BLOCKED

Missing capability:
  email.received

Provider:
  mail-mcp

Action:
  Install/enable provider
```

This is important because an automation is user intent and should survive connector lifecycle changes.

The UI should make the dependency explicit and provide a repair path.

## 8. Capability binding

At workflow validation time, resolve logical capabilities into providers.

Example:

```yaml
- uses: email.read
```

resolves to:

```text
Capability: email.read
Provider: mail-mcp
Provider instance: mail-mcp/default
Status: enabled
```

Persist the logical capability in the workflow definition. Do not persist only an MCP server ID.

Recommended runtime binding:

```go
type CapabilityBinding struct {
    Capability string
    ProviderID  string
    Kind        ProviderKind // builtin | mcp
    Status      CapabilityStatus
}
```

This allows the user to replace an MCP provider without rewriting every workflow.

## 9. Event subscription lifecycle

`when` triggers require more than action execution. They require event subscription.

For example:

```yaml
triggers:
  - when:
      event: email.received
```

At workflow enable time:

```text
Workflow enabled
      ↓
Resolve email.received
      ↓
Provider = mail-mcp
      ↓
Check provider lifecycle policy
      ↓
Register event subscription
```

If the MCP provider supports a native subscription mechanism, use it.

If it only exposes a polling tool, NusaShell may run a polling adapter.

The Automation Engine should not care which mechanism is underneath.

## 10. Disabled provider semantics for `when`

If the user disables the MCP provider used by an event trigger, the workflow becomes:

```text
BLOCKED
```

not:

```text
FAILED
```

The scheduler should not repeatedly create failed runs while the dependency is intentionally disabled.

Instead:

```text
Workflow enabled
      ↓
Dependency disabled
      ↓
Workflow BLOCKED
      ↓
No event subscription
      ↓
User enables provider
      ↓
Resolve capability
      ↓
Restore subscription
      ↓
Workflow ACTIVE
```

This significantly reduces noise and unnecessary retries.

## 11. MCP auto-start policy

Auto-start should be configurable per MCP server and optionally overridable per automation.

Server-level setting:

```text
Auto-start when required: ON
```

Automation-level behavior:

```text
inherit
always_require_active
allow_auto_start
```

Recommended default:

```text
allow_auto_start
```

If the user explicitly disabled the server, do not auto-start it. `off` is an intentional user constraint and must override workflow demand.

Therefore distinguish:

```text
disabled_by_user
not_running
starting
active
crashed
missing
```

`not_running` can auto-start.

`disabled_by_user` becomes `BLOCKED`.

`missing` becomes `BLOCKED` with an installation/repair action.

## 12. Agent behavior

The agent must see capability state when creating or debugging automations.

Example tool result:

```json
{
  "capability": "email.received",
  "provider": "mail-mcp",
  "kind": "mcp",
  "status": "blocked",
  "reason": "provider_disabled"
}
```

The agent can then tell the user:

> This automation requires `email.received`, provided by `mail-mcp`. The MCP server is currently disabled, so the automation is blocked.

It should not invent a replacement implementation or continuously retry the workflow.

## 13. Built-in tools and MCP tools share the same registry concept

NusaShell already has a toolbox/tool registry model. Automation should reuse that conceptual registry rather than creating a completely independent provider registry.

The registry should be able to answer:

```text
What capabilities exist?
Who provides them?
What are their input/output schemas?
Can they emit events?
Are they available?
Can their provider be auto-started?
What permissions do they require?
```

Conceptually:

```go
type Capability interface {
    Name() string
    Kind() CapabilityKind
    InputSchema() Schema
    OutputSchema() Schema
    Provider() ProviderRef
}
```

Actions add execution:

```go
type ActionCapability interface {
    Capability
    Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
```

Events add subscription:

```go
type EventCapability interface {
    Capability
    Subscribe(ctx context.Context, sink EventSink) error
    Unsubscribe(ctx context.Context) error
}
```

## 14. Workflow validation

Validation should happen at three levels.

### Syntax validation

Is the workflow definition structurally valid?

### Capability validation

Does every referenced action/event capability exist?

### Availability validation

Is its provider currently available or able to auto-start?

Example result:

```text
VALID
  syntax: OK
  capabilities: OK
  providers: OK
```

or:

```text
BLOCKED
  syntax: OK
  capabilities: OK
  provider: mail-mcp
  status: disabled_by_user
```

or:

```text
INVALID
  capability `email.send` does not exist
```

These states must not be conflated.

## 15. UI dependency display

The workflow editor should expose dependencies explicitly.

Example:

```text
Actions

✓ vps.health.check
  Built-in

✓ telegram.send
  MCP · telegram-mcp
  Auto-start enabled
```

For blocked dependencies:

```text
⚠ email.received
  MCP · mail-mcp
  Disabled

  This automation will remain blocked until the provider is enabled.

  [Enable provider]
```

Do not hide this behind a generic settings page. A workflow's external dependencies are part of its operational state.

## 16. Uninstall flow

When uninstalling an MCP provider, NusaShell should first discover dependent automations.

Example confirmation:

```text
Remove mail-mcp?

3 automations depend on capabilities from this provider:

• Invoice processor
• Customer email triage
• Daily inbox summary

Removing the provider will NOT delete these automations.
They will become BLOCKED until another provider supplies the
required capabilities.

[Cancel] [Remove and block automations]
```

This preserves user intent while making the consequence explicit.

## 17. Provider replacement

A logical capability should be replaceable.

Example:

```text
email.read
    │
    ├── mail-mcp       disabled
    └── outlook-mcp    available
```

The user can rebind the capability without editing every workflow.

This is another reason workflow definitions should reference `email.read`, not `mail-mcp.email.read`.

## 18. Event source security

An MCP event source must be treated as an external trust boundary.

Before enabling an event-driven automation, show:

```text
Trigger:
email.received

Provider:
mail-mcp

This provider can read:
• mailbox metadata
• incoming email metadata/content

The resulting event may start:
• shell commands
• network actions
• agent execution
```

A workflow should declare or derive required capabilities so the user can review the complete chain:

```text
Event capability
       ↓
Workflow
       ↓
Actions
       ↓
Required permissions
```

## 19. Recommended capability states

Use these states consistently across backend, agent tools, and UI:

```text
available
starting
not_running
disabled
missing
error
```

Map them to workflow availability:

```text
available              -> runnable
starting + auto-start  -> pending provider
not_running + auto-start -> pending provider
not_running + no auto-start -> blocked
 disabled              -> blocked
missing                -> blocked
error                  -> blocked/error depending on recovery policy
```

Do not expose provider process state directly as workflow state.

## 20. Final rule

The definitive rule for NusaShell automation is:

> **A trigger/action name identifies a logical capability. The capability may be implemented by NusaShell itself or by an MCP provider. The Automation Engine resolves that capability at runtime. If the provider is available, execute or subscribe. If it is merely stopped and auto-start is allowed, start it. If the user explicitly disabled it or it has been uninstalled, mark the dependent automation BLOCKED rather than repeatedly failing it.**

This keeps Automation Engine, Tool/MCP infrastructure, and workflow definitions loosely coupled while giving the user a predictable lifecycle when integrations are installed, stopped, disabled, replaced, or removed.
