'use strict';

const { spawn } = require('node:child_process');
const path = require('node:path');

const electron = require('electron');
const { electronDevArgs } = require('./runtime.cjs');

const appRoot = path.resolve(__dirname, '..');
const electronArgs = [...electronDevArgs(), appRoot, ...process.argv.slice(2)];

if (electronArgs.includes('--no-sandbox')) {
  console.warn('[nusashell] Linux Electron sandbox helper is not configured; using --no-sandbox for development only.');
  console.warn('[nusashell] For a sandboxed launch, configure chrome-sandbox as root:root with mode 4755.');
}

const child = spawn(electron, electronArgs, {
  env: process.env,
  stdio: 'inherit',
  windowsHide: true,
});

child.once('error', (error) => {
  console.error(`[nusashell] failed to start Electron: ${error.message}`);
  process.exitCode = 1;
});

child.once('exit', (code, signal) => {
  process.exitCode = signal ? 1 : (code ?? 1);
});
