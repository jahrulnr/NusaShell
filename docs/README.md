# NusaShell documentation

Start with the [NusaShell Guide](index.md) for installation and task-based tutorials. It covers model providers, ACP agents, projects, chat channels, browser tools, and scheduled automations.

## Developer reference

- [CLI guide](cli-guide.md) — installation, setup, providers, agents, and service commands.
- [Architecture](architecture.md) — layers, ownership, and the path of a conversation turn.
- [Domain model](domain-model.md) — conversations, providers, agents, tools, and invariants.
- [Adapters](adapters.md) — provider protocols and available tools.
- [Persistence](persistence.md) — conversation files, settings, and local storage.
- [Extending NusaShell](extending.md) — adding a provider adapter or tool.
- [WebSocket protocol](api/websocket.md) — frames and streaming conversation events.

The guides are Markdown in this directory. Edit the relevant tutorial with product changes; the documentation workflow checks the site on pull requests and publishes it to [GitHub Pages](https://jahrulnr.github.io/NusaShell/) after updates reach `master`.

## Preview the guide locally

Install the pinned documentation toolchain in a virtual environment:

```bash
python3 -m venv .venv-docs
source .venv-docs/bin/activate
python -m pip install -r requirements-docs.txt
```

Then run `make docs-serve` for a live preview or `make docs-build` to run the same strict build used by CI. Add new pages to `zensical.toml` so they appear in the navigation. Keep product tutorials under `docs/tutorials/` and update them alongside behavior changes.
