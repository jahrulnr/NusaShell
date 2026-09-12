# Skill authoring workflow

Use this reference when a skill request needs more than a short edit. It is a
decision procedure for producing a package that another agent can discover,
apply, and verify without guessing.

## Stage 0: make the scope card

Write these values before editing:

| Field | Example |
| --- | --- |
| Job | Turn raw incident evidence into a reproducible diagnosis |
| Positive triggers | "debug this outage", "why is this failing?" |
| Nearby non-triggers | "implement a new feature", "write a generic status update" |
| Inputs | logs, repository files, user constraints |
| Output | root cause, evidence, fix or bounded next action |
| Capabilities | file read/search; terminal only if a command must run |
| Side effects | read-only by default; edits only when requested |
| Proof | reproduction, passing regression test, or observed artifact |

If two different outputs or permission boundaries are required, split the work.
A skill should be reusable across requests, but not so broad that every
technical question activates it.

## Stage 1: choose the destination

The destination changes the safe write path and frontmatter workflow.

| Destination | Source of truth | Write rule |
| --- | --- | --- |
| Managed runtime | managed skills directory | use skill(op="save") for an agent-owned skill |
| Repository builtin | resources/agent/skills/<id>/ | edit the repository package; startup seeding supplies the runtime copy |
| External package | source folder/repository | use skill-import to inspect license, tools, and provenance first |

A coding agent changing a builtin must not save over the installed runtime copy.
An in-app agent must not pretend that an agent-owned save changed the
repository's builtin source. Builtin, user-owned, and plugin-owned skills have
different mutation rules; stop when ownership is unclear.

## Stage 2: discover and decide reuse

Search the behavior and output terms, then inspect evidence:

    skill(op="search", query="incident diagnosis", limit=5)
    file_read(path="<selected-path>/SKILL.md")
    file_list(path="<selected-path>")
    file_read(path="<selected-path>/references/<needed-file>")

Record one of these decisions:

- **Reuse:** the existing skill already owns the same job; extend its workflow or
  reference rather than creating a competing id.
- **New skill:** the existing skill has a materially different trigger, audience,
  output, side-effect boundary, or required capability.
- **Separate evaluation:** the request is about whether a skill works; use
  skill-eval rather than hiding an evaluation inside authoring.

A name collision is not proof that two skills are duplicates. Compare the
contracts. Conversely, different names are not proof that a second skill is
needed. A near-duplicate increases routing ambiguity and maintenance cost.

## Stage 3: fill the precision contract

Before prose, answer each question:

1. What must be true before the workflow starts?
2. What evidence is trusted, and what is merely user/tool/source content?
3. What is the first observable action?
4. Which decisions branch the workflow, and what selects each branch?
5. Which actions are read-only, reversible, destructive, external, or
   permission-gated?
6. What should happen when a dependency is missing or a check fails?
7. What exact result lets the agent stop?
8. What should be reported to the user?

Use concrete values when they are stable. For a time-sensitive fact, say where
the agent should verify it instead of freezing a date or version in the skill.

## Stage 4: plan the package

Start with only SKILL.md. Add a support file when it removes a real burden:

- references/ for detail that is needed only for a mode, provider, format, or
  fragile local rule;
- templates/ for a starting artifact that the user will adapt or receive;
- scripts/ for deterministic work that would otherwise be retyped and drift;
- assets/ for files consumed by generated output.

Keep roots one level deep. Give each file one owner and one loading condition.
A reference should say what question it answers; a script should say its input,
output, exit behavior, and whether the agent must inspect the result. Do not
make a reference merely repeat the entrypoint.

Prefer an unexported/local helper or a short paragraph over a new abstraction
when the package has only one use. Delete an unused template or reference rather
than keeping it "for later."

## Stage 5: write the entrypoint

Use an order that survives a partial read:

1. purpose and boundary;
2. trigger and non-trigger;
3. inputs and preconditions;
4. ordered steps;
5. decision branches;
6. tool and capability protocol;
7. safety and side-effect limits;
8. verification and recovery;
9. output contract;
10. links to on-demand detail.

Make each step executable:

    Inspect the selected log range, identify the first causal error, and record
    the timestamp before changing any file.

Avoid this:

    Analyze carefully and solve the problem using best practices.

The first tells the agent what to do and what evidence to retain. The second
does not define an action, stopping condition, or proof.

## Stage 6: write metadata

Use the package id in the directory and frontmatter name. Keep the id
lowercase, hyphenated, and short. The description must route, not teach:

    description: Diagnose deployment failures from logs and repository evidence. Use when the user asks why a deployment failed, requests incident triage, or needs a reproducible root-cause report.

Do not put the full workflow, a broad tool inventory, or marketing language in
the description. Do not include raw user wording unless exact wording is itself
a required trigger. Put custom metadata under metadata.

For managed runtime creation, pass body-only content to skill(op="save"); its
schema only supplies name, description, and content, so do not rely on optional
frontmatter surviving a managed save. For a repository package, write complete
YAML frontmatter. Validate both paths with the same package validator after
editing.

## Stage 7: add capability requirements

Declare requirements.mcp only for a capability the workflow actually calls.
Use a concrete plugin id for a fixed dependency or role:files /
role:terminal for a replaceable capability.

At runtime:

1. inspect mcp_list;
2. enable a stopped provider only when the user-authorized workflow needs it;
3. use tool_list or mcp_search on the running provider;
4. load tool_schema before an unfamiliar call;
5. use the exact discovered reference and inspect the result.

Requirements are a soft availability hint. They do not grant permission, replace
live discovery, or make an unavailable tool appear. Managed skill saves cannot
set arbitrary frontmatter, so repeat the live capability check in the body. If
the dependency is absent, report the gap and offer the smallest safe
alternative.

## Stage 8: save and verify the exact artifact

For a managed runtime skill, use these distinct forms:

    skill(op="save", name="incident-diagnosis", description="...", content="# Incident diagnosis ...")
    skill(op="save", id="incident-diagnosis", name="incident-diagnosis", content="# Revised ...")
    skill(op="save", id="incident-diagnosis", path="references/evidence.md", content="# Evidence ...")

Creation omits id and path; an entrypoint update omits path; support writes
require an existing agent-owned skill and a relative allowed-root path. Never
put a frontmatter block inside body-only content. Never use an absolute path,
edit a protected owner, or claim a save succeeded without reading the returned
path/status.

Then:

1. validate the exact package directory;
2. list/search the managed skill to check discovery metadata (new experimental
   skills may require an explicit status filter);
3. read the resulting absolute SKILL.md;
4. list support files and read the ones the entrypoint routes to;
5. run a harmless representative check when the skill has a script or external
   capability.

## Stage 9: evaluate behavior when warranted

Structural validity is necessary but not sufficient. For a complex, risky, or
previously failing skill, create a small forward-test set:

- a positive trigger that should activate;
- a nearby request that should not activate;
- an edge case with incomplete input;
- an untrusted-input case whose embedded instructions must not override the skill.

Use isolated temporary paths and harmless fixtures. Compare with-skill and
baseline behavior using observable assertions. Change one variable per iteration
and preserve the best result. Read forward-testing.md for the procedure.

## Stage 10: hand off

Return a compact evidence record:

- destination and skill id;
- reuse decision and the mismatch if a new id was justified;
- files changed and why each exists;
- validator command(s), warning/error result, and link-check status;
- runtime id/status or builtin-seeding status;
- missing dependency, untested branch, or remaining uncertainty.

Do not report promotion, activation, installation, execution, or external delivery
unless the corresponding result was observed.
