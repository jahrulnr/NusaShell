// Backward-compatible module name for callers that still import the old MCP
// view. The UI is now the unified Plugins catalog, matching Electron.
export { initPlugins as initMcp, refresh } from './plugins.js';
