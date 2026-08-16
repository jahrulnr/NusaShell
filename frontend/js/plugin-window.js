// Plugin window: renders plugin UIs inside the shell in a movable, resizable
// window, mirroring the Electron BrowserWindow instead of relying on
// window.open (whose size features browsers ignore → fullscreen tabs).
//
// Sizing follows the manifest's ui.window: mode "fullscreen" covers the
// shell; "panel"/"widget" use defaultSize (fallback 700x700 / 380x280) and
// honor `resizable`.

let dragState = null;

export function openPluginWindow({ id, name, ui }) {
  const win = document.getElementById('plugin-window');
  if (!win) return;
  const frame = document.getElementById('plugin-window-frame');
  const title = document.getElementById('plugin-window-title');

  const windowConfig = ui?.window ?? {};
  const mode = windowConfig.mode || 'panel';
  const isFullscreen = mode === 'fullscreen';
  const width = windowConfig.defaultSize?.width || (mode === 'widget' ? 380 : 700);
  const height = windowConfig.defaultSize?.height || (mode === 'widget' ? 280 : 700);

  win.classList.toggle('is-fullscreen', isFullscreen);
  win.style.resize = windowConfig.resizable === false ? 'none' : 'both';
  win.style.width = isFullscreen ? '' : `${width}px`;
  win.style.height = isFullscreen ? '' : `${height}px`;
  if (!isFullscreen) {
    win.style.left = `${Math.max(12, Math.round((window.innerWidth - width) / 2))}px`;
    win.style.top = `${Math.max(12, Math.round((window.innerHeight - height) / 3))}px`;
  }
  title.textContent = name;
  frame.src = `/plugins/${id}/`;
  win.hidden = false;
  document.getElementById('plugin-window-close').focus();
}

export function closePluginWindow() {
  const win = document.getElementById('plugin-window');
  if (!win) return;
  const frame = document.getElementById('plugin-window-frame');
  frame.src = 'about:blank';
  win.hidden = true;
}

export function wirePluginWindow() {
  const win = document.getElementById('plugin-window');
  if (!win) return;
  document.getElementById('plugin-window-close')?.addEventListener('click', closePluginWindow);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !win.hidden) closePluginWindow();
  });

  const bar = document.getElementById('plugin-window-titlebar');
  bar.addEventListener('pointerdown', (event) => {
    if (event.target.closest('#plugin-window-close')) return;
    if (win.classList.contains('is-fullscreen')) return;
    dragState = { dx: event.clientX - win.offsetLeft, dy: event.clientY - win.offsetTop };
    bar.setPointerCapture(event.pointerId);
  });
  bar.addEventListener('pointermove', (event) => {
    if (!dragState) return;
    const x = Math.min(Math.max(0, event.clientX - dragState.dx), window.innerWidth - 60);
    const y = Math.min(Math.max(0, event.clientY - dragState.dy), window.innerHeight - 60);
    win.style.left = `${x}px`;
    win.style.top = `${y}px`;
  });
  bar.addEventListener('pointerup', () => { dragState = null; });
  bar.addEventListener('pointercancel', () => { dragState = null; });
}
