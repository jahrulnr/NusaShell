# Description patterns

The description is discovery metadata. It should route a request to the skill,
not carry the workflow.

## Formula

Use this shape:

    <Action verb> <specific object or outcome>. Use when <recognizable user language, artifact, or context>.

Prefer one or two natural trigger phrases over a long keyword list. Keep the
description third-person, specific, and below 1024 characters.

## Good

    description: Diagnose deployment failures from logs and repository evidence. Use when the user asks why a deployment failed, requests incident triage, or needs a reproducible root-cause report.

    description: Create accessible responsive web interfaces from an existing product brief. Use when the user asks to build or reshape a frontend page, component, or user flow.

The action, scope, and trigger are visible before the body is loaded.

## Weak

    description: Helpful skill for technical work.

    description: This skill knows everything about APIs, files, testing, and deployment.

These descriptions are too broad to route reliably and promise no checkable
outcome.

## Common mistakes

- Listing tools instead of describing the user outcome.
- Repeating the entire workflow in the description.
- Using a raw incident sentence as a permanent trigger without generalizing it.
- Omitting nearby non-triggers, causing collisions with a more specific skill.
- Naming a provider, version, endpoint, or date that should be checked at runtime.
- Saying the skill can perform an action when it only explains that action.

For an existing skill, compare the old and proposed descriptions against positive
and nearby negative prompts. Change the description alone during a trigger
experiment so the effect is measurable.
