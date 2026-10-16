import { workspaceName } from './live-data.js?v=6';
import { createLoadingController } from '../../shared/loading-layer.js?v=1';

function icon(document, name) {
  const element = document.createElement('i');
  element.dataset.lucide = name;
  element.setAttribute('aria-hidden', 'true');
  return element;
}

function pathCrumbs(path) {
  const value = String(path || '');
  if (value === 'windows:computer') return [{ label: 'This PC', path: value }];
  if (/^[A-Za-z]:\\/.test(value)) {
    const parts = value.slice(3).split('\\').filter(Boolean);
    const drive = value.slice(0, 2);
    return [
      { label: 'This PC', path: 'windows:computer' },
      { label: drive, path: `${drive}\\` },
      ...parts.map((part, index) => ({ label: part, path: `${drive}\\${parts.slice(0, index + 1).join('\\')}` })),
    ];
  }
  const parts = value.split('/').filter(Boolean);
  return [{ label: '/', path: '/' }, ...parts.map((part, index) => ({ label: part, path: `/${parts.slice(0, index + 1).join('/')}` }))];
}

export function initWorkspaceBrowser(root, { rpc, onApply = () => {}, initialPath = '' } = {}) {
  if (typeof rpc !== 'function') throw new TypeError('workspace browser requires rpc');
  const document = root.ownerDocument;
  const configView = root.querySelector('#model-config-view');
  const browserView = root.querySelector('#workspace-browser-view');
  const trigger = root.querySelector('#workspace-browser-trigger');
  const back = root.querySelector('#workspace-browser-back');
  const up = root.querySelector('#workspace-up');
  const breadcrumbs = root.querySelector('#workspace-breadcrumbs');
  const search = root.querySelector('#workspace-search');
  const folderList = root.querySelector('#workspace-folder-list');
  const empty = root.querySelector('#workspace-empty');
  const currentPathLabel = root.querySelector('#workspace-current-path');
  const apply = root.querySelector('#workspace-use-folder');
  const loading = createLoadingController(folderList, { label: 'Loading folders' });
  let currentPath = initialPath;
  let parentPath = '';
  let entries = [];
  let generation = 0;

  function showBrowser(open) {
    configView.hidden = open;
    browserView.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
    if (open) search.focus();
    else trigger.focus();
  }

  function render() {
    breadcrumbs.replaceChildren();
    pathCrumbs(currentPath).forEach((crumb, index) => {
      if (index) {
        const separator = document.createElement('span');
        separator.textContent = '›';
        separator.setAttribute('aria-hidden', 'true');
        breadcrumbs.append(separator);
      }
      const button = document.createElement('button');
      button.type = 'button';
      button.dataset.breadcrumbPath = crumb.path;
      button.textContent = crumb.label;
      button.addEventListener('click', () => load(crumb.path));
      breadcrumbs.append(button);
    });

    folderList.replaceChildren(...entries.map((entry) => {
      const button = document.createElement('button');
      button.className = 'workspace-folder';
      button.type = 'button';
      button.dataset.folderPath = entry.path;
      button.append(icon(document, currentPath === 'windows:computer' ? 'hard-drive' : 'folder'));
      const copy = document.createElement('span');
      const name = document.createElement('strong');
      const pathLabel = document.createElement('small');
      name.textContent = entry.name || workspaceName(entry.path);
      pathLabel.textContent = entry.path;
      copy.append(name, pathLabel);
      button.append(copy, icon(document, 'chevron-right'));
      button.addEventListener('click', () => load(entry.path));
      return button;
    }));

    up.disabled = !parentPath;
    currentPathLabel.textContent = currentPath === 'windows:computer' ? 'This PC' : currentPath;
    apply.disabled = !currentPath || currentPath === 'windows:computer';
    empty.hidden = entries.length > 0;
    empty.textContent = 'No folders here';
    search.value = '';
    breadcrumbs.scrollLeft = breadcrumbs.scrollWidth;
    document.defaultView?.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
  }

  async function load(path = '') {
    const request = ++generation;
    folderList.setAttribute('aria-busy', 'true');
    empty.hidden = false;
    empty.textContent = 'Loading folders…';
    try {
      const result = await loading.run(() => rpc('agent.workspace.list-dirs', { path }));
      if (request !== generation) return;
      currentPath = result.path || path;
      parentPath = result.parent || '';
      entries = Array.isArray(result.entries) ? result.entries : [];
      render();
    } catch (error) {
      if (request !== generation) return;
      entries = [];
      render();
      empty.hidden = false;
      empty.textContent = error?.message || 'Could not read this folder';
    } finally {
      if (request === generation) folderList.setAttribute('aria-busy', 'false');
    }
  }

  trigger.addEventListener('click', () => { showBrowser(true); void load(currentPath); });
  back.addEventListener('click', () => showBrowser(false));
  up.addEventListener('click', () => { if (parentPath) void load(parentPath); });
  search.addEventListener('input', () => {
    const query = search.value.trim().toLowerCase();
    for (const folder of folderList.children) folder.hidden = !folder.textContent.toLowerCase().includes(query);
    const anyVisible = [...folderList.children].some((folder) => !folder.hidden);
    empty.hidden = anyVisible || (!query && entries.length > 0);
    empty.textContent = query ? 'No matching folders' : 'No folders here';
  });
  apply.addEventListener('click', () => {
    if (!currentPath || currentPath === 'windows:computer') return;
    trigger.querySelector('strong').textContent = workspaceName(currentPath);
    trigger.querySelector('[data-workspace-path]').textContent = currentPath;
    onApply(currentPath);
    showBrowser(false);
  });
  browserView.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    showBrowser(false);
  });

  const controller = {
    setPath(path) {
      currentPath = path || '';
      trigger.querySelector('strong').textContent = workspaceName(currentPath);
      trigger.querySelector('[data-workspace-path]').textContent = currentPath || 'Host home';
    },
  };
  controller.setPath(initialPath);
  return controller;
}
