import { resolveDroppedFilePath } from '../../js/desktop-file-path.js';

function attachmentKey(file) {
  if (!file) return '';
  if (file.type === 'folder') return `folder\u0000${file.file_path}`;
  const name = String(file.name || '');
  const size = Number.isFinite(file.size) ? file.size : '';
  const modified = Number.isFinite(file.lastModified) ? file.lastModified : '';
  const type = String(file.type || '');
  return `${name}\u0000${size}\u0000${modified}\u0000${type}`;
}

export function filesFromDataTransfer(transfer) {
  const files = [];
  const seen = new Set();
  const directories = new Set();
  const add = (file) => {
    if (!file || typeof file !== 'object') return;
    const key = attachmentKey(file);
    if (seen.has(key)) return;
    seen.add(key);
    files.push(file);
  };

  for (const item of Array.from(transfer?.items || [])) {
    if (item?.kind !== 'file') continue;
    const file = item.getAsFile?.();
    if (item.webkitGetAsEntry?.()?.isDirectory) directories.add(file);
    else add(file);
  }
  for (const file of Array.from(transfer?.files || [])) {
    if (!directories.has(file)) add(file);
  }
  return files;
}

export function mergeFiles(existing = [], incoming = []) {
  const merged = [];
  const seen = new Set();
  for (const file of [...existing, ...incoming]) {
    if (!file || typeof file !== 'object') continue;
    const key = attachmentKey(file);
    if (seen.has(key)) continue;
    seen.add(key);
    merged.push(file);
  }
  return merged;
}

// Reuse the desktop path bridge for directory drops; ordinary files stay byte attachments.
export async function folderAttachmentsFromDataTransfer(transfer, onUnavailable = () => {}) {
  const directories = Array.from(transfer?.items || []).filter(item => item?.kind === 'file')
    .map(item => ({ entry: item.webkitGetAsEntry?.(), file: item.getAsFile?.() }))
    .filter(({ entry }) => entry?.isDirectory);
  const folders = [];
  for (const { entry, file } of directories) {
    const nativeFile = file || (typeof entry.file === 'function'
      ? await new Promise(resolve => entry.file(resolve, () => resolve(null))) : null);
    const name = entry.name || nativeFile?.name || 'Folder';
    const path = resolveDroppedFilePath(nativeFile);
    if (!path) { onUnavailable(name); continue; }
    folders.push({ type: 'folder', name, media_type: 'inode/directory', file_path: path });
  }
  return folders;
}
