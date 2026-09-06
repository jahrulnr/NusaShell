# AGENTS.md - Prompt Engineering

These instructions apply to all agents and models working on this project.

## Core Principle

When designing or modifying prompts for agents, keep the prompts focused on what the target agent needs to know to perform its role.

Do not expose internal orchestration details, implementation architecture, tool inventories, delegation mechanics, or other metadata unless that information is directly required for the target agent to perform its task correctly.

The target agent should receive instructions about its **role, objective, constraints, available capabilities when relevant, expected behavior, and output requirements**. It should not receive a description of how the surrounding agent system is implemented.

## Prompt Construction Rules

When writing a system prompt or user prompt for another agent:

1. **Describe the role, not the architecture.**

   Tell the agent what it is responsible for doing. Do not explain how the agent is instantiated, delegated, orchestrated, or connected to other agents unless that directly affects its task.

2. **Describe required capabilities, not the entire toolbox.**

   Only mention a tool, function, API, or capability when the target agent needs to use it or needs to understand its behavior.

   Do not tell the agent that it has access to a larger toolbox simply because the runtime happens to expose those capabilities.

3. **Do not expose implementation details.**

   Avoid unnecessary references to:

   * Internal agent names or types.
   * Parent or sibling agents.
   * Agent spawning mechanisms.
   * Internal delegation protocols.
   * Tool inventories.
   * Context hydration.
   * Prompt construction.
   * Internal state management.
   * Security implementation details that do not affect the agent's task.
   * Internal routing or orchestration logic.
   * Runtime architecture.

4. **Do not describe capabilities merely because they exist.**

   The fact that a capability is available does not mean it belongs in the prompt.

   Include a capability only when the target agent needs to know about it in order to make a correct decision or perform an action.

5. **Avoid meta-explanations.**

   Do not explain why the prompt contains certain instructions, how the prompt was generated, or how the surrounding system works.

   Write the instruction directly.

6. **Keep prompts operational.**

   A good prompt should answer:

   * What is the agent responsible for?
   * What should it accomplish?
   * What constraints must it follow?
   * What information does it need?
   * What actions is it expected or allowed to take?
   * What should it produce?

   Anything outside these questions should be included only when it materially affects execution.

## Bad vs. Good Prompt Design

### Bad

```text
This exploratory background mode receives the same full conversation toolbox as the conversation agent, plus `learn()` for the typed catalog result. Direct tool side effects are enabled, including file CRUD, skill save/delete, memory_project writes, ACP/internal delegation, automation, and mcp_call. Learning-agent-specific security restrictions are intentionally deferred. Still obey the stage scoping rules below: do not use a tool outside the stage you are currently in, even when the toolbox contains it.
```

This exposes implementation and orchestration details that are irrelevant to the agent's actual objective.

### Good

```text
You are responsible for analyzing the completed work and identifying reusable knowledge that should be retained for future tasks.

Only record knowledge that is useful, generalizable, and supported by the available evidence. Follow the stage-specific constraints when deciding what information may be retained.
```

The second version communicates the behavior the agent needs without describing the architecture surrounding it.

## Information Boundary

When constructing a prompt, apply this rule:

**If the target agent does not need to know something to make a correct decision or perform its assigned task, do not put it in the prompt.**

In particular, do not expose information simply because it is available to the parent agent or runtime.

There is an important distinction between:

* **Capability:** what the runtime technically allows.
* **Permission:** what the agent is allowed to do.
* **Instruction:** what the agent should do.
* **Implementation detail:** how the surrounding system makes those capabilities available.

Prompts should communicate the relevant capability, permission, and instruction. They should generally omit the implementation detail.

## Tool and Capability Disclosure

When a tool or capability is necessary, describe it at the minimum useful abstraction level.

Prefer:

```text
You can save the resulting knowledge for future use.
```

over:

```text
You have access to `learn()`, which is exposed by the background agent runtime and returns a typed catalog result.
```

Prefer:

```text
You may update the project's persistent knowledge when the information meets the retention criteria.
```

over:

```text
You have access to `memory_project.write` through the parent agent's delegated toolbox.
```

The target agent should understand **what it can accomplish**, not the internal mechanism used to accomplish it.

## Prompt Separation

Keep system-level instructions, task-specific user instructions, and internal orchestration metadata conceptually separate.

The system prompt should define stable behavior, role, constraints, and capabilities that are relevant to the agent.

The user prompt should provide the current task, context, inputs, and expected result.

Internal orchestration information should remain outside the prompt unless the target agent genuinely needs it.

Do not use the system or user prompt as a dump of runtime state.

## Stage and Workflow Instructions

When an agent operates through multiple stages, describe the stages only to the extent necessary for the agent to execute them correctly.

Do not explain the entire orchestration pipeline or enumerate capabilities belonging to other stages.

For example, prefer:

```text
During this stage, analyze the result and identify knowledge worth retaining.
```

instead of:

```text
You are currently in the learning stage of the background orchestration pipeline. The parent agent has transitioned you from the exploration stage and has exposed the full toolbox, although some capabilities belong to later stages.
```

The first provides an actionable constraint. The second exposes architecture that does not help the agent perform its job.

## General Rule

Prompts are an interface between the orchestration system and the target agent.

Treat that interface as an abstraction boundary.

Expose the information required for correct behavior. Hide information that exists only because of the implementation behind the interface.

**Do not make an agent aware of the machinery surrounding it unless that awareness is necessary for its task.**
