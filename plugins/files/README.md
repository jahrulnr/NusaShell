# NusaShell Files Plugin

A bundled example plugin exposing a file-system MCP server (read, write, list,
tree, search, grep, patch, move, copy, delete, info, append, exists, touch).

## Path resolution

All file operations resolve paths through `resolvePath(root, input)` in
`mcp/config.js`:

- **Empty input** → the root directory (`NUSASHELL_FILES_ROOT` /
  `NUSASHELL_WORKSPACE` / user home, or via MCP Roots in-process).
- **`/` and absolute paths** → OS-absolute paths (the agent is a trusted actor
  operating on behalf of the user).
- **Relative paths** → resolved against the root; `../` traversal is allowed
  (escape is permitted).

There is **no containment jail**. Security is the user/AI provider's
responsibility — see
[docs/architecture/security-boundary.md](../../docs/architecture/security-boundary.md).

### Production runs the bundle

The manifest runs `node mcp/server.cjs` — the **esbuild bundle**, not the
source. After editing `mcp/config.js`, `mcp/fs-service.js`, `mcp/tools.js`, or
anything they import, rebuild so the shipped artifact matches the source:

```bash
cd plugins/files
npm run build   # regenerates mcp/server.cjs
npm test        # unit tests
```

### What is NOT in scope

- A `System32` denylist or any path-based containment — the root is a
  convenience for relative path resolution, not a jail.
- Changing the root default away from home (product decision); this plugin
  only resolves paths, it does not choose the root.
