# Skills

A skill is a markdown instruction pack the agent can load on demand. Skills
are stored in `skills.json` in the data directory.

## Authoring

Each skill has a name, a short description and markdown content. The
description is what the agent sees in the tool listing, so make it specific:
what the skill does and when to use it.

Keep instructions imperative and self-contained; the content is injected
verbatim into the agent's context when the skill is loaded.

## Using skills

- `skill_list` — enumerate skills.
- `skill_run` — load a skill's content by name; the agent then follows its
  instructions for the rest of the turn.

The Skills workspace also has a **Run** button that opens the skill in a new
conversation as its system context.
