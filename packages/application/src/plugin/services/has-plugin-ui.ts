/**
 * Whether a plugin view/list item exposes a UI surface (a window entry).
 * Headless MCP-only plugins omit `ui` and return false here so the launcher
 * can keep them off the Home grid while still listing them in the Plugins view.
 */
export function hasPluginUi(view: { readonly ui?: { readonly entry?: string } }): boolean {
  return Boolean(view.ui?.entry?.trim());
}
