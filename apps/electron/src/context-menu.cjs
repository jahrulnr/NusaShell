'use strict';

// buildContextMenuTemplate keeps the Electron context menu aligned with the
// browser's edit actions while the web shell continues to own application UI.
// Electron fills in the platform-native labels and accelerators for these roles.
function buildContextMenuTemplate({ isEditable = false, selectionText = '' } = {}) {
  const hasSelection = typeof selectionText === 'string' && selectionText.length > 0;

  return [
    { role: 'undo', enabled: isEditable },
    { role: 'redo', enabled: isEditable },
    { role: 'cut', enabled: isEditable && hasSelection },
    { role: 'copy', enabled: hasSelection },
    { role: 'paste', enabled: isEditable },
    { type: 'separator' },
    { role: 'selectAll' },
  ];
}

module.exports = { buildContextMenuTemplate };
