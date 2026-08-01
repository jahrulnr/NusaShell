# NusaShell Files Plugin

A bundled example plugin exposing a sandboxed file-system MCP server (read,
write, list, tree, search, grep, patch, move, copy, delete, info, append).

## Sandbox containment contract

All file operations are sandboxed to a single root directory:

- `NUSASHELL_FILES_ROOT` env var, or the user's home directory as fallback.
- `resolvePath(root, input)` in `mcp/config.js` rejects any path that escapes
  the root via `..` traversal or absolute paths outside the root.

### Production runs the bundle

The manifest runs `node mcp/server.cjs` — the **esbuild bundle**, not the
source. Containment must live in the **shipped artifact**, not just `config.js`.
A stale bundle that predates the `resolvePath` guard reintroduces the escape
silently. After editing `mcp/config.js` or anything it imports:

```bash
cd plugins/files
npm run build   # regenerates mcp/server.cjs
npm test        # includes bundle-containment.test.js (regression guard)
```

`tests/bundle-containment.test.js` asserts the shipped `server.cjs` contains
the containment guard, and `packages/infrastructure/tests/files-bundle-sandbox.test.ts`
spawns the bundle over stdio MCP and verifies `../../` and absolute paths are
rejected at runtime. Both must stay green.

### What is NOT in scope

- A `System32` denylist — pure containment under the configured root is the
  contract. An OS `EACCES` on an allowed-in-root path is unrelated to escape.
- Changing the root default away from home (product decision); this plugin
  only enforces containment, it does not choose the root.
