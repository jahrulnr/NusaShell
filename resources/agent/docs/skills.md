# Skills

A skill is a markdown instruction pack. Each skill lives at
`<datadir>/skills/<id>/SKILL.md` with git-style snapshots under
`versions/<n>/` and `meta.json` as the source of truth for status, version,
and origin.

## Status and origin

Status: `candidate` → `experimental` → `validated` → `trusted` →
`deprecated` → `retired`.

Default hydration and `skill(op="list")` / `search` return **routable**
skills (`trusted` and `validated`) unless you pass `status`.

Origin: `user`, `builtin`, `plugin`, `learned`. Learned skills must not
shadow curated ids; colliding ids are prefixed `learned-`.

Priority when ids collide: `user` > `builtin` > `plugin:<id>`. Shadowed
rows still appear in list (dimmed) but resolution uses the winner.

## Agent tools

The `skill` dispatcher:

- `list {limit?,status?}` — routable by default; includes `path`, `owned_by`,
  `status`, `version`, `bundled`
- `search {query,limit?,status?}` — discovery metadata only, never SKILL.md
- `save {name,content,description?,id?,path?}` — learned **experimental**
  skill (new or `versions/N+1`). `path` writes a support file inside an
  already mutable learned skill. Cannot overwrite trusted curated skills.
- `delete {id,owned_by?}` — learned `candidate` or `experimental` only

There is no `skill_run`. After discovery, `file_read` the absolute
`SKILL.md` before following it. List support files with `file_list`.

Good examples:

    skill(op="search", query="release checklist")
    file_read(path="<skill.path>/SKILL.md")
    skill(op="save", name="learned-nginx-reload", content="1. …")

Bad examples:

    skill(op="save", id="builtin-skill", content="overwrite trusted body")
    skill(op="delete", id="user-skill")
    follow search snippets without file_read of SKILL.md

## Human promote

The Skills workspace shows status, version, **Promote** (experimental or
validated → trusted), and **Rollback** to an immutable snapshot. Agents
never promote. The skill evolver cannot mark trusted; the evaluator never
sets trusted either. `skill.updated` events carry `op` (`evolve` from a
learning job, `promote` from this UI) so telemetry can tell queued
evolution from a human promotion.

Path layout:

| Owner | Directory |
| --- | --- |
| `user` / `builtin` / `learned` | `<datadir>/skills/<id>/` |
| `plugin:<plugin-id>` | `<datadir>/plugins/<plugin-id>/skills/<id>/` |

Each directory contains `SKILL.md`, `meta.json`, `versions/<n>/SKILL.md`,
and optional `references/`, `scripts/`, `templates/`, `examples/`.
