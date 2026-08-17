// Launch a plugin UI in a separate browser window.
//
// Plugin UIs are served same-origin at /plugins/<id>/ (the server injects the
// window.shell shim), so window.open loads them directly. We deliberately open
// a real browser window instead of an in-shell overlay: the previous overlay
// rendered fullscreen plugins on top of — effectively replacing — the
// NusaShell UI. Opening a separate window keeps the launcher and the plugin
// independent, matching the launcher's "opens in a separate browser window"
// contract.
//
// Returns true when a window was opened, false when the browser blocked the
// pop-up so callers can surface a hint.

export function openPluginWindow({ id, name, ui }) {
  if (!id) return false;
  const url = `/plugins/${id}/`;
  // A stable per-plugin target reuses/focuses an existing window instead of
  // stacking duplicates when the tile is clicked again.
  const target = `nusashell-plugin:${id}`;
  let win = null;
  try {
    win = window.open(url, target, windowFeatures(ui?.window));
  } catch {
    win = null;
  }
  if (!win) return false;
  try {
    win.focus();
  } catch {
    /* focus is best-effort */
  }
  if (name) {
    try {
      win.document.title = name;
    } catch {
      /* document may not be ready yet; the plugin sets its own <title> */
    }
  }
  return true;
}

// windowFeatures maps the manifest ui.window to window.open features.
// Fullscreen plugins open as a normal browser window/tab (the browser controls
// the size); panel/widget request their default size. Browsers only honor
// width/height when `popup` is set, so non-fullscreen sizes use popup=yes.
export function windowFeatures(windowConfig = {}) {
  const mode = windowConfig?.mode || 'panel';
  if (mode === 'fullscreen') return '';
  const width = Number(windowConfig?.defaultSize?.width) || (mode === 'widget' ? 380 : 700);
  const height = Number(windowConfig?.defaultSize?.height) || (mode === 'widget' ? 280 : 700);
  return `popup=yes,width=${width},height=${height}`;
}
