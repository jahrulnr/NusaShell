// Resolve the absolute path for a dropped folder when the page is running in
// Electron. The browser intentionally has no general filesystem-path API, so
// the normal web path remains empty and the composer asks the user to use the
// workspace picker instead.

export function resolveDroppedFilePath(file, desktopApi = defaultDesktopApi()) {
  try {
    const path = desktopApi?.getPathForFile?.(file);
    if (typeof path === 'string' && path) return path;
  } catch {
    // A missing or unavailable preload bridge must not break ordinary drops.
  }

  // Electron versions before 32 exposed File.path directly. Keep this small
  // compatibility path for existing installations while modern Electron uses
  // webUtils.getPathForFile through preload.
  return typeof file?.path === 'string' ? file.path : '';
}

// The preload bridge is exposed only by the Electron renderer. Keep runtime
// detection tied to the narrow capability already used by desktop uploads.
export function isElectronRuntime(desktopApi = defaultDesktopApi()) {
  return typeof desktopApi?.getPathForFile === 'function';
}

function defaultDesktopApi() {
  return typeof window !== 'undefined' ? window.nusashellDesktop : null;
}
