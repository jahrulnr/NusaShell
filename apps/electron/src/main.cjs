'use strict';

const { app, BrowserWindow, Menu, dialog, shell } = require('electron');
const { spawn } = require('node:child_process');
const path = require('node:path');

const {
  buildBackendEnvironment,
  getFreePort,
  isExternalHTTPURL,
  isSameOriginURL,
  normalizeLoopbackURL,
  resolveBackendPath,
  waitForURL,
} = require('./runtime.cjs');

const STARTUP_TIMEOUT_MS = 30000;
const repositoryRoot = path.resolve(__dirname, '..', '..', '..');
const preloadPath = path.join(__dirname, 'preload.cjs');

if (process.env.NUSASHELL_ELECTRON_USER_DATA) {
  app.setPath('userData', path.resolve(process.env.NUSASHELL_ELECTRON_USER_DATA));
}

let mainWindow = null;
let backendProcess = null;
let backendStopping = false;
let applicationQuitting = false;
let applicationURL = null;

const hasSingleInstanceLock = app.requestSingleInstanceLock();

if (!hasSingleInstanceLock) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  });

  app.whenReady().then(() => {
    // The web application owns all visible navigation. Do not expose
    // Electron's default File/Edit application menu above it.
    Menu.setApplicationMenu(null);
    return startApplication();
  }).catch((error) => {
    failStartup(error);
  });

  app.on('before-quit', () => {
    applicationQuitting = true;
    stopBackend();
  });

  app.on('window-all-closed', () => {
    app.quit();
  });
}

async function startApplication() {
  applicationURL = await startBackendOrUseConfiguredURL();
  configureWebContentsPolicy();
  mainWindow = createMainWindow(applicationURL);
}

async function startBackendOrUseConfiguredURL() {
  const configuredURL = process.env.NUSASHELL_ELECTRON_URL;
  if (configuredURL) {
    const url = normalizeLoopbackURL(configuredURL);
    await waitForURL(url, { timeoutMs: STARTUP_TIMEOUT_MS });
    return url;
  }

  const backendPath = resolveBackendPath({
    explicitPath: process.env.NUSASHELL_ELECTRON_BACKEND,
    packaged: app.isPackaged,
    resourcesPath: process.resourcesPath,
    repositoryRoot,
  });
  if (!backendPath) {
    throw new Error(
      'NusaShell Go backend was not found. Install the NusaShell core release first, '
      + 'or set NUSASHELL_ELECTRON_BACKEND to an external nusashell binary.',
    );
  }

  const port = await getFreePort();
  const url = normalizeLoopbackURL(`http://127.0.0.1:${port}/`);
  const spawnOptions = {
    cwd: app.isPackaged ? path.dirname(backendPath) : repositoryRoot,
    env: buildBackendEnvironment(process.env, port, app.isPackaged),
    stdio: ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  };
  // Windows user-local installs expose a .cmd launcher beside the versioned
  // binary. Node needs a shell to execute that launcher; direct .exe paths do
  // not use this branch.
  if (process.platform === 'win32' && /\.(?:cmd|bat)$/i.test(backendPath)) {
    spawnOptions.shell = true;
  }
  const child = spawn(backendPath, [], spawnOptions);
  backendProcess = child;
  attachBackendLogging(child);
  let backendLaunchError = null;
  child.once('error', (error) => {
    backendLaunchError = error;
    if (!backendStopping) console.error('[nusashell] backend process error:', error);
  });
  child.once('exit', (code, signal) => {
    if (backendStopping || applicationQuitting) return;
    const reason = signal ? `signal ${signal}` : `exit code ${code}`;
    console.error(`[nusashell] backend stopped unexpectedly (${reason})`);
    if (mainWindow && !mainWindow.isDestroyed()) {
      dialog.showErrorBox('NusaShell stopped', `The local NusaShell backend stopped (${reason}).`);
    }
    app.quit();
  });

  try {
    await waitForURL(url, {
      timeoutMs: STARTUP_TIMEOUT_MS,
      isStopped: () => {
        if (backendLaunchError) throw backendLaunchError;
        return child.exitCode !== null || child.signalCode !== null;
      },
    });
  } catch (error) {
    stopBackend();
    throw error;
  }
  return url;
}

function attachBackendLogging(child) {
  child.stdout?.on('data', (chunk) => process.stdout.write(`[nusashell] ${chunk}`));
  child.stderr?.on('data', (chunk) => process.stderr.write(`[nusashell] ${chunk}`));
}

function stopBackend() {
  if (!backendProcess || backendStopping) return;
  backendStopping = true;
  try {
    backendProcess.kill();
  } catch (error) {
    if (error.code !== 'ESRCH') console.error('[nusashell] failed to stop backend:', error);
  }
}

function createMainWindow(url) {
  const window = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 920,
    minHeight: 600,
    show: false,
    autoHideMenuBar: true,
    backgroundColor: '#121512',
    webPreferences: secureWebPreferences(),
  });

  window.once('ready-to-show', () => window.show());
  window.on('closed', () => {
    if (mainWindow === window) mainWindow = null;
  });
  window.loadURL(url.toString()).catch((error) => {
    failStartup(error);
  });
  return window;
}

function secureWebPreferences() {
  return {
    preload: preloadPath,
    contextIsolation: true,
    nodeIntegration: false,
    sandbox: true,
    webSecurity: true,
    spellcheck: true,
  };
}

function configureWebContentsPolicy() {
  app.on('web-contents-created', (_event, contents) => {
    contents.setWindowOpenHandler(({ url }) => {
      if (isSameOriginURL(url, applicationURL)) {
        return {
          action: 'allow',
          overrideBrowserWindowOptions: {
            webPreferences: secureWebPreferences(),
          },
        };
      }
      openExternalURL(url);
      return { action: 'deny' };
    });

    contents.on('will-navigate', (event, url) => {
      if (isSameOriginURL(url, applicationURL)) return;
      event.preventDefault();
      openExternalURL(url);
    });

    contents.on('will-redirect', (event, url) => {
      if (isSameOriginURL(url, applicationURL)) return;
      event.preventDefault();
      openExternalURL(url);
    });
  });
}

function openExternalURL(value) {
  if (!isExternalHTTPURL(value)) return;
  shell.openExternal(value).catch((error) => {
    console.error('[nusashell] failed to open external URL:', error);
  });
}

function failStartup(error) {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`[nusashell] failed to start: ${message}`);
  if (app.isReady()) dialog.showErrorBox('NusaShell could not start', message);
  stopBackend();
  app.quit();
}
