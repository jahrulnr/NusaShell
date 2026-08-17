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
  const screen = (typeof window !== 'undefined' && window.screen) ? window.screen : {};
  let win = null;
  try {
    win = window.open(url, target, windowFeatures(ui?.window, {
      width: screen.availWidth || screen.width,
      height: screen.availHeight || screen.height,
    }));
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

// windowFeatures maps the manifest ui.window to window.open features. It always
// requests a real, separate browser window (a popup) rather than a new tab:
// Chromium/Firefox open a tab when the features string is empty, but open a
// distinct window when `popup` plus width/height are set. Fullscreen uses the
// available screen size; panel/widget use their default size. The window is
// centered on screen. `screen` (availWidth/availHeight) is injected so this
// stays a pure, testable function.
export function windowFeatures(windowConfig = {}, screen = {}) {
  const mode = windowConfig?.mode || 'panel';
  const screenW = Math.max(320, Number(screen.width) || 1280);
  const screenH = Math.max(320, Number(screen.height) || 800);
  const declaredW = Number(windowConfig?.defaultSize?.width) || 0;
  const declaredH = Number(windowConfig?.defaultSize?.height) || 0;

  let width;
  let height;
  if (mode === 'fullscreen') {
    // Cover the whole available screen (still a separate window).
    width = screenW;
    height = screenH;
  } else if (mode === 'widget') {
    width = declaredW || 380;
    height = declaredH || 280;
  } else {
    // panel (default): honor a declared size, else pick a size that scales with
    // the screen (a comfortable fraction, capped) so it is neither a tiny fixed
    // popup nor full screen — it adapts to the display.
    width = declaredW || Math.min(1100, Math.round(screenW * 0.8));
    height = declaredH || Math.min(820, Math.round(screenH * 0.85));
  }

  // Clamp to the screen and center horizontally / slightly above center.
  width = Math.max(320, Math.min(width, screenW));
  height = Math.max(240, Math.min(height, screenH));
  const left = Math.max(0, Math.round((screenW - width) / 2));
  const top = Math.max(0, Math.round((screenH - height) / 3));
  // `popup=yes` + explicit size makes browsers open a real separate window
  // rather than a new tab.
  return `popup=yes,width=${width},height=${height},left=${left},top=${top}`;
}
