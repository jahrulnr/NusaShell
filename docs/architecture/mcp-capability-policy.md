# MCP capability policy

NusaShell adopts MCP capabilities by protocol stability, not by whether a
library happens to expose an API. This keeps the shell broker small and gives
operators and agents a single capability vocabulary.

## Implement now: stable server primitives

| Capability | MCP operations | NusaShell direction |
| --- | --- | --- |
| Tools | `tools/list`, `tools/call` | Brokered only through `PluginRuntimeManager`. Agent uses progressive discovery to stay below provider tool limits. |
| Prompts | `prompts/list`, `prompts/get` | Expose progressively through `mcp_context`; retrieving a prompt provides context and never executes it as a tool. |
| Resources | `resources/list`, `resources/templates/list`, `resources/read` | Expose as bounded shell-managed context through `mcp_context`; binary content is not injected into the model. |
| Completion | `completion/complete` | Expose only after a prompt or resource template is discovered, with bounded completion results. |
| Logging / progress | protocol notifications | Forward sanitized diagnostics to the shell log UI; progress is presentation only until task support is adopted. |

The control model is intentional: prompts are user-controlled, resources are
application-controlled, and tools are model-controlled. The shell still brokers
every request and does not let plugin UIs or providers connect directly to MCP.

## Knowledge only: do not enable yet

| Capability | Status | Why it is deferred |
| --- | --- | --- |
| Tasks | Experimental in the 2025-11-25 revision; evolving as an extension | Requires durable task storage, polling, cancellation, task ownership, and UI states. |
| Elicitation, form mode | Stable capability but not enabled by NusaShell yet | Requires clear server attribution, review/edit/decline UI, and input handling policy. |
| Elicitation, URL mode | Recently introduced and still evolving | Requires consent, URL/domain presentation, and safe external-navigation handling. |
| Sampling | Stable capability but not enabled by NusaShell yet | A server can request model generation and tools; it needs a human approval and cost-control design. |
| Roots | Under active protocol evolution/deprecation discussion | Do not establish a public NusaShell contract around it yet. |
| MCP Apps / UI extensions | Extension ecosystem, not a core MVP primitive | Evaluate after the broker/lifecycle and plugin UI contract are proven. |

“Skills” are not an MCP core primitive. NusaShell may later support its own
`SKILL.md` knowledge convention, but it remains separate from MCP prompts and
resources so that the protocol boundary stays clear.

## Adoption gate

Before moving a deferred capability into the implementation column, require:

1. A stable official specification and SDK support appropriate to the selected
   MCP transports.
2. A typed application port and brokered transport path.
3. User-facing approval UX where the capability can request model work, user
   data, navigation, or a long-running operation.
4. Unit and integration tests for unsupported-capability rejection and the
   successful negotiated path.
5. Documentation updates here and in `docs/blueprint.md`.

## Agent knowledge rule

Agents may reason from this document about deferred capabilities, but must not
claim that NusaShell supports them or request them from an MCP server until the
capability is moved to **Implement now**.
