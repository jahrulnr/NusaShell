'use strict';

const { spawn, spawnSync } = require('node:child_process');
const { existsSync } = require('node:fs');
const path = require('node:path');

const testFile = path.join(__dirname, 'electron-ui.test.mjs');
const testArgs = ['--test', testFile];
const environment = {
  ...process.env,
  NUSASHELL_ELECTRON_UI: '1',
};

// Some restricted npm environments defer Electron's postinstall download
// until its CLI is first invoked. Materialize the pinned runtime before the
// Playwright test tries to launch it.
const electronCLI = path.join(__dirname, '..', 'node_modules', '.bin', process.platform === 'win32' ? 'electron.cmd' : 'electron');
const electronBootstrap = spawnSync(electronCLI, ['--version'], {
  env: environment,
  stdio: 'inherit',
  windowsHide: true,
});
if (electronBootstrap.error || electronBootstrap.status !== 0) {
  console.error(`failed to initialize Electron: ${electronBootstrap.error?.message || `exit ${electronBootstrap.status}`}`);
  process.exitCode = 1;
}

if (process.exitCode) {
  // Do not launch Xvfb/Node after the runtime bootstrap failed.
  process.exit(1);
}

// Linux CI and headless developer sessions can still run the real Electron
// renderer when Xvfb is available. The test itself remains a normal Node test
// on macOS and Windows, where the desktop display is provided by the runner.
if (process.platform === 'linux'
    && !process.env.DISPLAY
    && !process.env.WAYLAND_DISPLAY
    && !process.env.NUSASHELL_ELECTRON_XVFB) {
  const xvfb = '/usr/bin/xvfb-run';
  if (!existsSync(xvfb)) {
    console.error('Electron UI tests need DISPLAY/WAYLAND_DISPLAY or xvfb-run.');
    process.exitCode = 1;
  } else {
    const child = spawn(xvfb, ['-a', process.execPath, ...testArgs], {
      env: { ...environment, NUSASHELL_ELECTRON_XVFB: '1' },
      stdio: 'inherit',
    });
    child.once('error', (error) => {
      console.error(`failed to start xvfb-run: ${error.message}`);
      process.exitCode = 1;
    });
    child.once('exit', (code, signal) => {
      process.exitCode = signal ? 1 : (code ?? 1);
    });
  }
} else {
  const child = spawn(process.execPath, testArgs, {
    env: environment,
    stdio: 'inherit',
  });
  child.once('error', (error) => {
    console.error(`failed to start Node test runner: ${error.message}`);
    process.exitCode = 1;
  });
  child.once('exit', (code, signal) => {
    process.exitCode = signal ? 1 : (code ?? 1);
  });
}
