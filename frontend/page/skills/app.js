import { rpc as defaultRPC, on as defaultOn } from '../../js/rpc.js';
import { confirmDialog, createSelect, debounce, el, toast } from '../../js/ui.js';
import { skillIsDeletable } from '../../js/views/skills.js';
import { renderMarkdownBody } from '../../shared/markdown-body.js?v=7';

import {
  buildSkillFileTree,
  detectSkillFileMime,
  filterSkills,
  formatSkillDate,
  formatSkillFileSize,
  skillOwnerLabel,
  skillOwnerTone,
  skillStatusLabel,
  skillStatusTone,
  skillVersionLabel,
  isBinarySkillFile,
  stripSkillFrontMatter,
  versionOptions,
} from './model.js';

const STATUS_HINTS = {
  candidate: 'A proposed skill that still needs review.',
  experimental: 'A learned skill that is still being evaluated.',
  validated: 'A reviewed skill ready for deliberate use.',
  trusted: 'A trusted skill included in normal agent context.',
  deprecated: 'Kept for history, but no longer recommended.',
  retired: 'Retired from normal use.',
};

function toBase64(buffer) {
  const bytes = new Uint8Array(buffer);
  let binary = '';
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
  }
  return btoa(binary);
}

export function createSkillsPage(root = document, { rpcClient = defaultRPC, onEvent = defaultOn } = {}) {
  const state = {
    skills: [],
    activeId: null,
    activeOwnedBy: '',
    activeFile: null,
    activeSkill: null,
    loading: true,
    error: null,
  };
  let initialized = false;
  let rollbackSelect = null;
  let refreshToken = 0;
  let readToken = 0;
  let mobilePaneToken = 0;
  let mobileFilePaneToken = 0;
  let unsubscribe = null;
  const expandedDirectories = new Set();
  let documentPointerHandler = null;
  let documentKeyHandler = null;

  const find = (id) => root.querySelector(`#${id}`);
  const selectedKey = (skill) => `${skill?.id || ''}:${skill?.owned_by || ''}`;
  const activeKey = () => `${state.activeId || ''}:${state.activeOwnedBy}`;
  const pageNode = () => root.nodeType === 1 ? root : root.querySelector('#skills-view');
  const isMobile = () => Number(window.innerWidth) <= 720;

  function setMobilePane(pane) {
    const workspace = find('skills-workspace');
    const page = pageNode();
    if (!workspace) return;
    const previousPane = workspace.dataset.mobilePane || 'list';
    const changed = previousPane !== pane;
    if (pane === 'detail' && previousPane === 'list' && page && isMobile()) {
      workspace.dataset.mobileListScroll = String(page.scrollTop || 0);
    }
    workspace.dataset.mobilePane = pane;
    if (page) page.dataset.mobilePane = pane;
    if (!changed) return mobilePaneToken;

    const token = ++mobilePaneToken;
    if (!isMobile() || !page) return token;
    if (pane === 'detail') {
      page.scrollTop = 0;
    } else if (pane === 'list') {
      const top = Number(workspace.dataset.mobileListScroll) || 0;
      window.setTimeout(() => {
        if (token !== mobilePaneToken || workspace.dataset.mobilePane !== 'list' || !isMobile()) return;
        page.scrollTop = top;
      }, 0);
    }
    return token;
  }

  function setMobileFilePane(pane) {
    const body = root.querySelector('.skill-sheet-body');
    const page = pageNode();
    if (!body) return mobileFilePaneToken;
    const previousPane = body.dataset.mobileFilePane || 'files';
    const changed = previousPane !== pane;
    if (pane === 'content' && previousPane === 'files' && page && isMobile()) {
      body.dataset.mobileFilesScroll = String(page.scrollTop || 0);
    }
    body.dataset.mobileFilePane = pane;
    if (!changed) {
      if (pane === 'content' && page && isMobile()) page.scrollTop = 0;
      return mobileFilePaneToken;
    }

    const token = ++mobileFilePaneToken;
    if (!isMobile() || !page) return token;
    if (pane === 'content') {
      page.scrollTop = 0;
    } else if (pane === 'files') {
      const top = Number(body.dataset.mobileFilesScroll) || 0;
      window.setTimeout(() => {
        if (token !== mobileFilePaneToken || body.dataset.mobileFilePane !== 'files' || !isMobile()) return;
        page.scrollTop = top;
        root.querySelector('.skill-file-row[aria-current="true"]')?.focus({ preventScroll: true });
      }, 0);
    }
    return token;
  }

  function focusMobileControl(id, token = mobilePaneToken) {
    if (!isMobile()) return;
    window.setTimeout(() => {
      if (token !== mobilePaneToken) return;
      find(id)?.focus({ preventScroll: true });
    }, 0);
  }

  function focusMobileFileControl(id, token = mobileFilePaneToken) {
    if (!isMobile()) return;
    window.setTimeout(() => {
      if (token !== mobileFilePaneToken || root.querySelector('.skill-sheet-body')?.dataset.mobileFilePane !== 'content') return;
      find(id)?.focus({ preventScroll: true });
    }, 0);
  }

  function resetMobileFilePane() {
    const body = root.querySelector('.skill-sheet-body');
    if (!body) return;
    mobileFilePaneToken += 1;
    body.dataset.mobileFilePane = 'files';
    delete body.dataset.mobileFilesScroll;
  }

  function setSyncState(copy, tone = '') {
    const node = find('skills-sync-state');
    if (!node) return;
    node.textContent = copy;
    node.dataset.state = tone;
  }

  function setActionsOpen(open) {
    const menu = find('skill-actions-menu');
    const trigger = find('skill-actions-trigger');
    if (!menu || !trigger) return;
    menu.hidden = !open;
    trigger.setAttribute('aria-expanded', String(open));
  }

  function toggleActions() {
    const trigger = find('skill-actions-trigger');
    setActionsOpen(trigger?.getAttribute('aria-expanded') !== 'true');
  }

  function updateCounters(visible = state.skills.length) {
    const total = state.skills.length;
    const count = find('skills-count');
    const indexCount = find('skills-index-count');
    const results = find('skills-results');
    const hasQuery = Boolean(find('skills-search')?.value.trim());
    if (count) count.textContent = `${total} skill${total === 1 ? '' : 's'}`;
    if (indexCount) indexCount.textContent = String(total);
    if (results) {
      results.textContent = total === visible ? `${total} shown` : `${visible} of ${total} shown`;
      results.hidden = !hasQuery;
    }
  }

  function listEmpty(title, detail, iconName = 'search-x') {
    const empty = el('div', { class: 'skills-list-empty' });
    empty.append(
      el('i', { dataset: { lucide: iconName }, 'aria-hidden': 'true' }),
      el('strong', { text: title }),
      el('span', { text: detail }),
    );
    return empty;
  }

  function renderList() {
    const list = find('skills-list');
    if (!list) return;
    const query = find('skills-search')?.value || '';
    const filtered = filterSkills(state.skills, query);
    list.replaceChildren();
    list.setAttribute('aria-busy', String(state.loading));
    updateCounters(filtered.length);

    if (state.error && !state.skills.length) {
      list.append(listEmpty('Skills unavailable', 'The capability library could not be reached. Try opening Skills again.', 'cloud-off'));
      return;
    }
    if (!filtered.length) {
      list.append(listEmpty(query ? 'No matching skills' : 'No skills installed', query ? 'Try a different name, owner, or purpose.' : 'Install a .skill package to give the agent another capability.'));
      return;
    }

    for (const skill of filtered) {
      const tone = skillStatusTone(skill);
      const item = el('div', {
        class: `skills-index-item${selectedKey(skill) === activeKey() ? ' is-active' : ''}${skill.shadowed ? ' is-shadowed' : ''}`,
        role: 'listitem',
      });
      const select = el('button', {
        class: 'skill-index-select',
        type: 'button',
        'aria-label': `${skill.name || skill.id} — ${skillOwnerLabel(skill)} — ${skillStatusLabel(skill)}`,
      });
      const mark = el('span', { class: 'skill-index-mark', 'aria-hidden': 'true' }, String(skill.name || skill.id || 'S').trim().slice(0, 1).toUpperCase());
      const copy = el('span', { class: 'skill-index-copy' });
      copy.append(
        el('strong', { text: skill.name || skill.id || 'Unnamed skill' }),
        el('p', { text: skill.description || 'No description' }),
        el('span', { class: 'skill-index-meta', text: `${skillVersionLabel(skill)} · ${skill.category || 'uncategorized'}` }),
      );
      const trail = el('span', { class: 'skill-index-trail' });
      trail.append(
        el('span', { class: 'skill-index-owner', text: skillOwnerLabel(skill), title: skillOwnerLabel(skill) }),
        el('span', { class: `skill-index-status skill-status-${tone}`, text: skillStatusLabel(skill), title: STATUS_HINTS[tone] }),
      );
      select.append(mark, copy, trail);
      select.addEventListener('click', () => {
        const paneToken = setMobilePane('detail');
        if (selectedKey(skill) === activeKey() && state.activeSkill) {
          focusMobileControl('skills-mobile-back', paneToken);
          return;
        }
        void selectSkill(skill.id, skill.owned_by);
        focusMobileControl('skills-mobile-back', paneToken);
      });
      item.append(select);
      list.append(item);
    }
    window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
  }

  function setEmptyState(title, detail) {
    const empty = find('skills-empty');
    if (!empty) return;
    empty.hidden = false;
    const heading = empty.querySelector('h2');
    const copy = empty.querySelector('p');
    if (heading) heading.textContent = title;
    if (copy) copy.textContent = detail;
  }

  function setReaderEmpty(title, detail) {
    const empty = find('skill-editor-empty');
    if (!empty) return;
    empty.hidden = false;
    const heading = empty.querySelector('strong');
    const copy = empty.querySelector('span');
    if (heading) heading.textContent = title;
    if (copy) copy.textContent = detail;
  }

  function clearReader() {
    const viewer = find('skill-file-viewer');
    if (viewer) viewer.hidden = true;
    const path = find('skill-file-path');
    const size = find('skill-file-size');
    const content = find('skill-file-content');
    if (path) path.textContent = '';
    if (size) size.textContent = '';
    if (content) content.textContent = '';
    setReaderEmpty('Select a package file', 'Choose a file on the left to inspect its source without leaving the skill sheet.');
  }

  function renderLifecycle(skill) {
    const wrap = find('skill-lifecycle');
    if (!wrap || !skill) return;
    wrap.hidden = false;
    setActionsOpen(false);
    const tone = skillStatusTone(skill);
    const status = find('skill-status-badge');
    const version = find('skill-version-meta');
    const promote = find('skills-promote');
    const rollback = find('skills-rollback');
    const remove = find('skills-delete');
    const guidance = find('skill-actions-guidance');
    const owner = String(skill.owned_by || skill.origin || '').trim();
    if (status) {
      status.className = `skill-status-badge skill-status-${tone}`;
      status.textContent = skillStatusLabel(skill);
      status.title = STATUS_HINTS[tone] || '';
    }
    if (version) version.textContent = skillVersionLabel(skill);
    if (guidance) {
      guidance.textContent = owner === 'builtin'
        ? 'Deleting this built-in skill prevents automatic reinstallation.'
        : owner.startsWith('plugin:')
          ? 'Uninstall the owning plugin to remove this skill.'
          : skillIsDeletable(skill)
            ? 'Delete removes this package and its version snapshots.'
            : 'This skill cannot be deleted from this view.';
    }
    if (promote) {
      const allowed = tone === 'experimental' || tone === 'validated';
      promote.hidden = !allowed;
      promote.disabled = !allowed;
    }
    if (remove) {
      const allowed = skillIsDeletable(skill);
      remove.hidden = !allowed;
      remove.disabled = !allowed;
    }
    const options = versionOptions(skill);
    const current = String(skill.active_version || skill.version || options[0]?.value || '1');
    const rawSelect = find('skills-rollback-version');
    if (rollbackSelect) {
      rollbackSelect.setData(options);
      rollbackSelect.setSelected([current]);
    } else if (rawSelect) {
      rawSelect.replaceChildren(...options.map((option) => el('option', { value: option.value, text: option.text })));
      rawSelect.value = current;
    }
    if (rollback) rollback.disabled = options.length === 0;
  }

  function renderSkillFiles(files = []) {
    const tree = find('skill-files-tree');
    const empty = find('skill-tree-empty');
    const meta = find('skill-tree-meta');
    const fileEntries = files.filter((file) => file?.type === 'file');
    const nodes = buildSkillFileTree(files);
    const fileCount = find('skill-file-count');
    if (fileCount) fileCount.textContent = `${fileEntries.length} file${fileEntries.length === 1 ? '' : 's'}`;
    if (!tree) return;
    tree.replaceChildren();
    tree.hidden = nodes.length === 0;
    if (meta) meta.textContent = fileEntries.length ? `${fileEntries.length} file${fileEntries.length === 1 ? '' : 's'}` : 'No files';
    if (empty) empty.hidden = nodes.length !== 0;
    if (!nodes.length) return;

    const directories = [];
    const collectDirectories = (items) => {
      for (const node of items) {
        if (node.type !== 'directory') continue;
        directories.push(node.path);
        collectDirectories(node.children);
      }
    };
    collectDirectories(nodes);
    if (!expandedDirectories.size) directories.forEach((path) => expandedDirectories.add(path));

    const fileCountIn = (node) => node.type === 'file'
      ? 1
      : node.children.reduce((total, child) => total + fileCountIn(child), 0);
    const renderNodes = (items, depth = 0) => {
      const fragment = document.createDocumentFragment();
      for (const node of items) {
        const item = el('div', {
          class: `skill-tree-node is-${node.type}`,
          role: 'listitem',
        });
        if (node.type === 'directory') {
          const expanded = expandedDirectories.has(node.path);
          const row = el('button', {
            class: `skill-file-row is-directory tree-depth-${Math.min(depth, 6)}`,
            type: 'button',
            'aria-expanded': String(expanded),
            'aria-label': `${node.path} folder — ${expanded ? 'Collapse' : 'Expand'}`,
          });
          row.append(
            el('i', { class: 'skill-tree-chevron', dataset: { lucide: expanded ? 'chevron-down' : 'chevron-right' }, 'aria-hidden': 'true' }),
            el('i', { dataset: { lucide: expanded ? 'folder-open' : 'folder' }, 'aria-hidden': 'true' }),
            el('span', { class: 'skill-file-path', text: node.name, title: node.path }),
            el('span', { class: 'skill-file-meta', text: `${fileCountIn(node)} file${fileCountIn(node) === 1 ? '' : 's'}` }),
          );
          row.addEventListener('click', () => {
            if (expandedDirectories.has(node.path)) expandedDirectories.delete(node.path);
            else expandedDirectories.add(node.path);
            renderSkillFiles(state.activeSkill?.files || []);
          });
          const children = el('div', { class: 'skill-file-children', role: 'list' });
          children.hidden = !expanded;
          children.append(renderNodes(node.children, depth + 1));
          item.append(row, children);
        } else {
          const row = el('button', {
            class: `skill-file-row is-file tree-depth-${Math.min(depth, 6)}${state.activeFile === node.path ? ' active' : ''}`,
            type: 'button',
            'aria-current': state.activeFile === node.path ? 'true' : 'false',
            'aria-label': `${node.path} — ${formatSkillFileSize(node.sizeBytes)}`,
          });
          row.append(
            el('i', { dataset: { lucide: 'file-text' }, 'aria-hidden': 'true' }),
            el('span', { class: 'skill-file-path', text: node.name, title: node.path }),
            el('span', { class: 'skill-file-meta', text: formatSkillFileSize(node.sizeBytes) }),
          );
          row.addEventListener('click', () => { void openSkillFile(node.path); });
          item.append(row);
        }
        fragment.append(item);
      }
      return fragment;
    };
    tree.append(renderNodes(nodes));
    window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
  }

  function renderSkill(skill) {
    const empty = find('skills-empty');
    const detail = find('skills-detail');
    if (empty) empty.hidden = true;
    if (detail) detail.hidden = false;
    const letter = find('skill-mark-letter');
    const title = find('skill-editor-title');
    const owner = find('skill-detail-owner');
    const category = find('skill-detail-category');
    const ownerStat = find('skill-owner-stat');
    const usageStat = find('skill-usage-stat');
    const lastUsedStat = find('skill-last-used-stat');
    if (letter) letter.textContent = String(skill.name || skill.id || 'S').trim().slice(0, 1).toUpperCase();
    if (title) title.textContent = skill.name || skill.id || 'Unnamed skill';
    if (owner) owner.textContent = skillOwnerLabel(skill);
    if (category) category.textContent = skill.category || 'Unsorted';
    if (ownerStat) {
      ownerStat.textContent = skillOwnerLabel(skill);
      ownerStat.dataset.owner = skillOwnerTone(skill);
    }
    if (usageStat) usageStat.textContent = `${Number(skill.usage_count) || 0} time${Number(skill.usage_count) === 1 ? '' : 's'}`;
    if (lastUsedStat) lastUsedStat.textContent = skill.last_used_at ? formatSkillDate(skill.last_used_at) : 'Not used yet';
    resetMobileFilePane();
    renderLifecycle(skill);
    renderSkillFiles(skill.files || []);
    clearReader();
  }

  function showEmpty() {
    state.activeId = null;
    state.activeOwnedBy = '';
    state.activeFile = null;
    state.activeSkill = null;
    expandedDirectories.clear();
    setActionsOpen(false);
    const detail = find('skills-detail');
    if (detail) detail.hidden = true;
    setMobilePane('list');
    resetMobileFilePane();
    const lifecycle = find('skill-lifecycle');
    if (lifecycle) lifecycle.hidden = true;
    const tree = find('skill-files-tree');
    if (tree) tree.replaceChildren();
    const emptyTree = find('skill-tree-empty');
    if (emptyTree) emptyTree.hidden = false;
    const meta = find('skill-tree-meta');
    if (meta) meta.textContent = 'No files';
    setEmptyState('Choose a skill', "Browse a skill's lineage, package files, and instruction source from one focused sheet.");
    renderList();
  }

  async function selectSkill(id, ownedBy = '') {
    if (!id) return false;
    state.activeId = id;
    state.activeOwnedBy = ownedBy || '';
    state.activeFile = null;
    state.activeSkill = null;
    expandedDirectories.clear();
    resetMobileFilePane();
    const token = ++readToken;
    renderList();
    const detail = find('skills-detail');
    const empty = find('skills-empty');
    if (empty) empty.hidden = true;
    if (detail) detail.hidden = false;
    const title = find('skill-editor-title');
    if (title) title.textContent = 'Loading skill…';
    try {
      const result = await rpcClient('skills.read', { id, owned_by: ownedBy || undefined });
      if (token !== readToken || activeKey() !== `${id}:${ownedBy || ''}`) return false;
      state.activeSkill = result.skill;
      renderSkill(result.skill);
      return true;
    } catch (error) {
      if (token !== readToken || activeKey() !== `${id}:${ownedBy || ''}`) return false;
      state.error = error;
      showEmpty();
      setSyncState('Read failed', 'error');
      return false;
    }
  }

  async function openSkillFile(path) {
    if (!state.activeId || !path) return;
    state.activeFile = path;
    const filePaneToken = setMobileFilePane('content');
    focusMobileFileControl('skill-file-back', filePaneToken);
    const token = ++readToken;
    renderSkillFiles(state.activeSkill?.files || []);
    const viewer = find('skill-file-viewer');
    if (viewer) {
      viewer.hidden = true;
      viewer.setAttribute('aria-busy', 'true');
    }
    setReaderEmpty('Loading file…', 'Reading the selected source from the skill package.');
    try {
      const result = await rpcClient('skills.file.read', {
        id: state.activeId,
        owned_by: state.activeOwnedBy || undefined,
        path,
      });
      if (token !== readToken) return;
      if (viewer) {
        viewer.hidden = false;
        viewer.setAttribute('aria-busy', 'false');
      }
      const filePath = find('skill-file-path');
      const fileSize = find('skill-file-size');
      const content = find('skill-file-content');
      const mime = detectSkillFileMime(path);
      const source = String(result.content || '');
      if (filePath) filePath.textContent = path;
      if (fileSize) fileSize.textContent = `${mime} · ${formatSkillFileSize(result.sizeBytes)}${result.truncated ? ' · truncated' : ''}`;
      if (content) {
        content.classList.remove('is-code', 'is-binary');
        if (isBinarySkillFile(path, source)) {
          content.classList.add('is-binary');
          content.replaceChildren(el('div', { class: 'skill-file-unavailable' }, [
            el('i', { dataset: { lucide: 'file-warning' }, 'aria-hidden': 'true' }),
            el('strong', { text: 'Preview not available' }),
            el('span', { text: `Binary file (${mime}) cannot be previewed here.` }),
          ]));
        } else if (mime === 'text/markdown') {
          await renderMarkdownBody(content, stripSkillFrontMatter(source));
        } else {
          content.classList.add('is-code');
          content.replaceChildren(el('pre', { class: 'skill-file-plain', text: source }));
        }
        window.lucide?.createIcons({ attrs: { 'stroke-width': 1.7 } });
      }
      const empty = find('skill-editor-empty');
      if (empty) empty.hidden = true;
    } catch (error) {
      if (token !== readToken) return;
      if (viewer) viewer.setAttribute('aria-busy', 'false');
      setReaderEmpty('Could not read file', error.message || 'The selected file is unavailable.');
      toast(error.message || 'Could not read skill file.', 'error');
    }
  }

  async function installSkill() {
    const input = find('skill-file-input');
    const file = input?.files?.[0];
    if (!file) return;
    input.value = '';
    try {
      const result = await rpcClient('skills.install', { data: toBase64(await file.arrayBuffer()), filename: file.name });
      toast(`Installed: ${result.name || result.id}`, 'success');
      await refresh();
      await selectSkill(result.id);
    } catch (error) {
      toast(error.message || 'Could not install skill.', 'error');
    }
  }

  async function promoteSkill() {
    if (!state.activeId) return;
    try {
      await rpcClient('skills.promote', { id: state.activeId, owned_by: state.activeOwnedBy || undefined });
      toast('Skill promoted.', 'success');
      await refresh();
    } catch (error) {
      toast(error.message || 'Promote failed.', 'error');
    }
  }

  async function rollbackSkill() {
    if (!state.activeId) return;
    const raw = rollbackSelect?.getSelected?.()?.[0] || find('skills-rollback-version')?.value;
    const version = Number(raw);
    if (!Number.isInteger(version) || version < 1) {
      toast('Pick a version to roll back to.', 'error');
      return;
    }
    const ok = await confirmDialog('Roll back this skill?', `Active version will move to v${version}. The current body is kept as history.`, 'Rollback', true);
    // The shared dialog traps focus but does not restore it; send it back to a
    // stable control once the overlay is gone.
    if (!ok) {
      find('skill-actions-trigger')?.focus();
      return;
    }
    try {
      await rpcClient('skills.rollback', { id: state.activeId, version, owned_by: state.activeOwnedBy || undefined });
      toast(`Rolled back to v${version}.`, 'success');
      await refresh();
      find('skill-actions-trigger')?.focus();
    } catch (error) {
      toast(error.message || 'Rollback failed.', 'error');
    }
  }

  async function deleteSkill() {
    if (!state.activeId || !skillIsDeletable(state.activeSkill)) return;
    const name = state.activeSkill?.name || state.activeId;
    const owner = String(state.activeSkill?.owned_by || state.activeSkill?.origin || '').trim();
    const reinstallNote = owner === 'builtin' ? ' NusaShell will not automatically reinstall this built-in skill.' : '';
    const ok = await confirmDialog('Delete this skill?', `“${name}” and its version snapshots will be removed.${reinstallNote}`, 'Delete');
    if (!ok) {
      find('skill-actions-trigger')?.focus();
      return;
    }
    try {
      await rpcClient('skills.delete', { id: state.activeId, owned_by: state.activeOwnedBy || undefined });
      toast('Skill deleted.', 'success');
      showEmpty();
      await refresh();
      find('skills-search')?.focus();
    } catch (error) {
      toast(error.message || 'Delete failed.', 'error');
    }
  }

  async function refresh() {
    const token = ++refreshToken;
    state.loading = true;
    state.error = null;
    setSyncState('Syncing…');
    renderList();
    try {
      const result = await rpcClient('skills.list');
      if (token !== refreshToken) return;
      state.skills = Array.isArray(result.skills) ? result.skills : [];
      state.loading = false;
      const selected = state.skills.find((skill) => selectedKey(skill) === activeKey());
      if (selected) await selectSkill(selected.id, selected.owned_by);
      else showEmpty();
      setSyncState('Synced', 'ready');
      renderList();
    } catch (error) {
      if (token !== refreshToken) return;
      state.loading = false;
      state.error = error;
      renderList();
      if (!state.activeSkill) showEmpty();
      setSyncState('Sync failed', 'error');
    }
  }

  // Draggable column splitters — mirrors production initSplitters()
  // (frontend/js/views/skills.js): the index-pane and file-nav widths persist
  // in px under skills-col-1 / skills-col-2 and drop out of the stacked
  // ≤720px layout. The elements are injected here so index.html stays the
  // shell's markup contract.
  function wireColumnSplitter({ container, pane, id, storageKey, min, max, defaultColumns, label }) {
    const SPLITTER_W = 6;
    const splitter = el('div', {
      class: 'skills-splitter',
      id,
      role: 'separator',
      'aria-orientation': 'vertical',
      'aria-label': label,
      title: 'Drag to resize',
      tabindex: '0',
      style: 'cursor: col-resize',
    });
    pane.after(splitter);
    const isNarrow = () => (typeof window.matchMedia === 'function'
      ? window.matchMedia('(max-width: 720px)').matches
      : window.innerWidth <= 720);
    const clear = () => {
      container.style.removeProperty('grid-template-columns');
      splitter.hidden = true;
    };
    const apply = (px) => {
      if (isNarrow()) {
        clear();
        return;
      }
      splitter.hidden = false;
      container.style.gridTemplateColumns = `${px}px ${SPLITTER_W}px minmax(0, 1fr)`;
    };
    const saved = Number.parseFloat(localStorage.getItem(storageKey) || '');
    const applySaved = () => {
      if (Number.isFinite(saved) && saved >= min && saved <= max) apply(saved);
      else if (isNarrow()) clear();
      else {
        splitter.hidden = false;
        container.style.gridTemplateColumns = defaultColumns;
      }
    };
    applySaved();
    window.addEventListener('resize', applySaved, { passive: true });
    let dragging = false;
    let startX = 0;
    let startW = 0;
    splitter.addEventListener('mousedown', (event) => {
      event.preventDefault();
      dragging = true;
      startX = event.clientX;
      startW = pane.getBoundingClientRect().width;
      splitter.classList.add('dragging');
      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
    });
    document.addEventListener('mousemove', (event) => {
      if (!dragging) return;
      apply(Math.max(min, Math.min(max, startW + event.clientX - startX)));
    });
    document.addEventListener('mouseup', () => {
      if (!dragging) return;
      dragging = false;
      splitter.classList.remove('dragging');
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
      localStorage.setItem(storageKey, `${Math.round(pane.getBoundingClientRect().width)}px`);
    });
    splitter.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      const next = Math.max(min, Math.min(max, pane.getBoundingClientRect().width + (event.key === 'ArrowRight' ? 16 : -16)));
      apply(next);
      localStorage.setItem(storageKey, `${Math.round(next)}px`);
    });
  }

  function initSplitters() {
    const workspace = find('skills-workspace');
    const index = workspace?.querySelector('.skills-index');
    if (workspace && index && !find('skills-splitter-1')) {
      wireColumnSplitter({
        container: workspace,
        pane: index,
        id: 'skills-splitter-1',
        storageKey: 'skills-col-1',
        min: 150,
        max: 450,
        defaultColumns: 'minmax(270px, 340px) 6px minmax(0, 1fr)',
        label: 'Resize skill library',
      });
    }
    const sheetBody = root.querySelector('.skill-sheet-body');
    const fileNav = sheetBody?.querySelector('.skills-file-nav');
    if (sheetBody && fileNav && !find('skills-splitter-2')) {
      wireColumnSplitter({
        container: sheetBody,
        pane: fileNav,
        id: 'skills-splitter-2',
        storageKey: 'skills-col-2',
        min: 150,
        max: 500,
        defaultColumns: 'minmax(190px, 245px) 6px minmax(0, 1fr)',
        label: 'Resize package file list',
      });
    }
  }

  function init() {
    if (initialized) return controller;
    initialized = true;
    setMobilePane('list');
    initSplitters();
    const search = find('skills-search');
    const install = find('skills-install');
    const input = find('skill-file-input');
    find('skills-mobile-back')?.addEventListener('click', () => {
      mobileFilePaneToken += 1;
      const paneToken = setMobilePane('list');
      if (isMobile()) window.setTimeout(() => {
        if (paneToken !== mobilePaneToken || find('skills-workspace')?.dataset.mobilePane !== 'list') return;
        root.querySelector('.skills-index-item.is-active .skill-index-select')?.focus({ preventScroll: true });
      }, 0);
    });
    find('skill-file-back')?.addEventListener('click', () => {
      setMobileFilePane('files');
    });
    search?.addEventListener('input', debounce(renderList, 150));
    install?.addEventListener('click', () => input?.click());
    input?.addEventListener('change', () => { void installSkill(); });
    find('skill-actions-trigger')?.addEventListener('click', (event) => {
      event.stopPropagation();
      toggleActions();
    });
    find('skills-promote')?.addEventListener('click', () => { setActionsOpen(false); void promoteSkill(); });
    find('skills-rollback')?.addEventListener('click', () => { setActionsOpen(false); void rollbackSkill(); });
    find('skills-delete')?.addEventListener('click', () => { setActionsOpen(false); void deleteSkill(); });
    const version = find('skills-rollback-version');
    if (version) rollbackSelect = createSelect(version, { data: [{ text: 'v1', value: '1' }], value: '1', placeholder: 'Version', search: false });
    documentPointerHandler = (event) => {
      if (!root.contains(event.target)) {
        setActionsOpen(false);
        return;
      }
      if (!event.target?.closest?.('#skill-actions-menu, #skill-actions-trigger')) setActionsOpen(false);
    };
    documentKeyHandler = (event) => {
      if (event.key !== 'Escape' || find('skill-actions-trigger')?.getAttribute('aria-expanded') !== 'true') return;
      event.preventDefault();
      setActionsOpen(false);
      find('skill-actions-trigger')?.focus();
    };
    document.addEventListener('pointerdown', documentPointerHandler);
    document.addEventListener('keydown', documentKeyHandler);
    if (typeof onEvent === 'function') {
      const refreshFromEvent = debounce(() => { void refresh(); }, 280);
      unsubscribe = onEvent('skill.updated', refreshFromEvent);
    }
    renderList();
    showEmpty();
    return controller;
  }

  function destroy() {
    mobilePaneToken += 1;
    mobileFilePaneToken += 1;
    unsubscribe?.();
    rollbackSelect?.destroy?.();
    if (documentPointerHandler) document.removeEventListener('pointerdown', documentPointerHandler);
    if (documentKeyHandler) document.removeEventListener('keydown', documentKeyHandler);
    unsubscribe = null;
    rollbackSelect = null;
    documentPointerHandler = null;
    documentKeyHandler = null;
    initialized = false;
  }

  const controller = { init, refresh, selectSkill, openSkillFile, destroy, state };
  return controller;
}
