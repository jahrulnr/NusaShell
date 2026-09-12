# Agentskills alignment

NusaShell uses the useful portable subset of the Agentskills-style package
without pretending to implement every draft platform field.

## Portable package contract

- A skill is a directory containing SKILL.md.
- SKILL.md starts with YAML frontmatter and a Markdown body.
- Frontmatter name and description are required.
- name is lowercase hyphen-case and matches the package id.
- description says what the skill does and when it applies.
- Detail is progressively disclosed from description to SKILL.md to one-level
  support files.

## NusaShell-compatible frontmatter

NusaShell preserves these optional top-level fields:

| Field | Use |
| --- | --- |
| compatibility | human-readable host or capability note |
| license | license identifier or attribution |
| metadata | custom descriptive data; keep it a mapping |
| requirements.mcp | soft plugin/role capability requirements |
| allowed-tools | accepted for Codex compatibility, not runtime-enforced |

Keep custom fields below metadata rather than inventing a new top-level schema.
The portable validator accepts the fields above and rejects unknown top-level
keys so a typo does not silently become routing metadata.

## Deliberate differences

NusaShell does not require or enforce:

- skill.yaml, tools.yaml, runtime.yaml, or an execution manifest;
- allowed-tools as a permission boundary;
- an agents/openai.yaml UI metadata file;
- a skill_exec or skill_run operation.

Codex-style UI metadata can be retained while working on a Codex package, but
it is not part of the NusaShell runtime package contract. A NusaShell builtin
must keep support content below references/, templates/, scripts/, or assets/.

## Portability rule

Write the body around the outcome and decision points. Mention capabilities only
at the minimum stable abstraction. When porting from another ecosystem, inspect
the source license and map its tools and paths with skill-import; do not copy an
untrusted or incompatible execution model into a NusaShell skill.
