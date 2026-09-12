// Small, reusable install-prompt controller shared by the full shell and the
// conversation prototype. The browser owns eligibility: the affordance stays
// hidden until beforeinstallprompt is delivered, while standalone and Electron
// runtimes are excluded up front.

import { isElectronRuntime } from './desktop-file-path.js';

function defaultWindow() {
  return typeof window !== 'undefined' ? window : null;
}

function isStandaloneWindow(windowRef) {
  if (!windowRef) return false;
  const displayMode = windowRef.matchMedia?.('(display-mode: standalone)')?.matches;
  return Boolean(displayMode || windowRef.navigator?.standalone === true);
}

function isElectronWindow(windowRef) {
  if (!windowRef) return false;
  const desktopBridge = windowRef.nusashellDesktop;
  const userAgent = String(windowRef.navigator?.userAgent || '');
  return isElectronRuntime(desktopBridge || null)
    || windowRef.process?.type === 'renderer'
    || /\bElectron\/\d/i.test(userAgent);
}

export function isInstallableWindow(windowRef = defaultWindow()) {
  return Boolean(windowRef?.addEventListener) && !isStandaloneWindow(windowRef) && !isElectronWindow(windowRef);
}

export function initInstallPrompt(button, {
  windowRef = defaultWindow(),
  onAccepted,
  onInstalled,
} = {}) {
  if (!button) return () => {};

  let deferredPrompt = null;
  const hide = () => { button.hidden = true; };
  const show = () => { button.hidden = false; };
  hide();

  if (!isInstallableWindow(windowRef)) return () => {};

  const handleBeforeInstallPrompt = (event) => {
    event.preventDefault?.();
    deferredPrompt = event;
    show();
  };

  const handleInstall = async () => {
    if (!deferredPrompt) return;
    const promptEvent = deferredPrompt;
    deferredPrompt = null;
    hide();
    try {
      await promptEvent.prompt?.();
      const choice = await promptEvent.userChoice;
      if (choice?.outcome === 'accepted') onAccepted?.(choice);
    } catch {
      // Browser-specific prompt/userChoice failures should not leave a stale
      // install affordance or interrupt the rest of the shell.
    }
  };

  const handleInstalled = () => {
    deferredPrompt = null;
    hide();
    onInstalled?.();
  };

  windowRef.addEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
  windowRef.addEventListener('appinstalled', handleInstalled);
  button.addEventListener('click', handleInstall);

  return () => {
    windowRef.removeEventListener('beforeinstallprompt', handleBeforeInstallPrompt);
    windowRef.removeEventListener('appinstalled', handleInstalled);
    button.removeEventListener('click', handleInstall);
    deferredPrompt = null;
    hide();
  };
}
