# Skill validator

The bundled scripts/quick_validate.py is a portable structural validator
modeled after the Codex skill creator validator and adapted to the NusaShell
package contract. It uses only the Python standard library.

## Commands

Run from the repository or from a directory that contains the skill package:

    python3 scripts/quick_validate.py <skill-dir>
    python3 scripts/quick_validate.py --check-links <skill-dir>
    python3 scripts/quick_validate.py --strict <skill-dir>
    python3 scripts/quick_validate.py --json <skill-dir> <another-skill-dir>

Use python when that is the executable provided by the host. A zero exit code
means no errors; --strict also makes warnings fail. The validator accepts
multiple directories, so a repository gate can pass all builtin directories
without accidentally passing the AGENTS.md file.

## Error checks

Errors block the normal gate:

- missing or non-directory skill path;
- missing or non-UTF-8 SKILL.md;
- directory or frontmatter name not lowercase hyphen-case, over 64 characters,
  or inconsistent with each other;
- missing/empty name, description, or body;
- malformed or unclosed frontmatter, duplicate keys, unsupported top-level keys,
  invalid scalar types, or invalid requirements.mcp;
- a second frontmatter block accidentally included in body-only content;
- an unfinished [TODO: ...] placeholder outside a fenced code block;
- symlinks in the package;
- local Markdown links that escape the package or point to missing files when
  --check-links is enabled.

The parser covers the small frontmatter subset NusaShell needs. Keep metadata
under the metadata map and requirements under requirements.mcp; unusual
full-YAML constructs should be simplified before validation.

## Warnings

Warnings do not fail the normal gate:

- SKILL.md is larger than the one-megabyte editable limit or over the
  recommended 500 body lines;
- a reference Markdown file is over 300 lines;
- an unsupported top-level entry is present;
- allowed-tools is present. It is accepted for Codex compatibility but is
  not enforced by NusaShell;
- angle brackets appear in a description. NusaShell accepts them, but they
  may indicate an unfinished placeholder.

Use --strict for a newly authored package when you want a clean package shape.
Do not use strict mode as a blanket retroactive gate until existing package
warnings have been reviewed.

## Machine-readable output

--json emits one report per input:

    [
      {
        "path": "...",
        "valid": true,
        "errors": [],
        "warnings": []
      }
    ]

Each issue includes severity, code, message, and an optional one-based line.
Keep CI logic based on the exit code; messages and codes are for diagnosis and
tooling.

## Link semantics

The optional link check resolves only relative links within the package.
External URLs, anchors, and root-relative site routes such as /docs are not
resolved. A relative link must stay inside the skill directory and point to an
existing file or directory.

## What it cannot prove

A green validator does not prove that:

- the description triggers at the right rate;
- the workflow is complete or non-contradictory;
- a script's algorithm is correct;
- a tool call exists or has the expected live schema;
- a remote side effect succeeded;
- the model will follow the instructions.

Use the authoring workflow and forward-test for those claims.

The importer has a separate lint_skill.py because it adds source-ecosystem
checks for unmapped tools, paths, and license adaptation. Use this validator
for the portable package contract; use the importer lint as an additional
adaptation check when bringing in an external skill.
