'use strict';

const { contextBridge, webUtils } = require('electron');

function getPathForFile(file) {
  // Keep the bridge narrow and validate the value before passing it to the
  // Electron API. Renderer code can only ask for the path of a real File
  // object supplied by a browser drag/drop event.
  if (typeof File === 'undefined' || !(file instanceof File)) return null;
  try {
    const filePath = webUtils.getPathForFile(file);
    return typeof filePath === 'string' && filePath ? filePath : null;
  } catch {
    return null;
  }
}

contextBridge.exposeInMainWorld('nusashellDesktop', {
  getPathForFile,
});
