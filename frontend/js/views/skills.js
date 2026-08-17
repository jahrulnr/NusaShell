// Skills workspace: catalog + file tree + file viewer.

import { rpc } from '../rpc.js';
import { el, debounce, toast } from '../ui.js';

const state = { skills: [], activeId: null, activeOwnedBy: '', activeFile: null };

export async function initSkills() {
  document.getElementById('skills-search').addEventListener('input', debounce(renderList, 150));
  document.getElementById('install-skill-btn').addEventListener('click', () => {
    document.getElementById('skill-file-input').click();
  });
  document.getElementById('skill-file-input').addEventListener('change', installSkill);
  initSplitters();
  await refresh();
}

async function installSkill(e) {
  const file = e.target.files?.[0];
  if (!file) return;
  e.target.value = ''; // reset for re-install
  try {
    const buf = await file.arrayBuffer();
    const b64 = btoa(String.fromCharCode(...new Uint8Array(buf)));
    const res = await rpc('skills.install', { data: b64, filename: file.name });
    toast(`Installed: ${res.name}`, 'success');
    await refresh();
    selectSkill(res.id);
  } catch (err) {
    toast(err.message, 'error');
  }
}

export async function refresh() {
  const { skills } = await rpc('skills.list');
  state.skills = skills;
  document.getElementById('skills-count').textContent = `${skills.length} skill${skills.length === 1 ? '' : 's'}`;
  renderList();
  if (state.activeId) {
    const still = skills.find((s) => s.id === state.activeId && (s.owned_by || '') === state.activeOwnedBy);
    if (!still) { state.activeId = null; state.activeOwnedBy = ''; showEmpty(); }
    else await selectSkill(state.activeId, state.activeOwnedBy);
  }
}

function renderList() {
  const list = document.getElementById('skills-list');
  const q = document.getElementById('skills-search').value.toLowerCase();
  const filtered = state.skills.filter((s) => s.name.toLowerCase().includes(q) || (s.description || '').toLowerCase().includes(q));
  list.innerHTML = '';
  if (!filtered.length) {
    list.append(el('div', { class: 'skills-empty' },
      el('strong', { text: q ? 'No matching skills' : 'No skills yet' }),
      el('span', { text: q ? 'Try a different search.' : 'Skills are managed via the file system.' }),
    ));
    return;
  }
  for (const s of filtered) {
    const ownerBadge = s.owned_by && s.owned_by !== 'user'
      ? el('span', { class: `skill-owner-badge ${s.owned_by.startsWith('plugin:') ? 'plugin' : s.owned_by}`, text: s.owned_by.replace('plugin:', '') })
      : null;
    const item = el('button', {
      class: `skills-list-item${s.id === state.activeId && (s.owned_by || '') === state.activeOwnedBy ? ' active' : ''}${s.shadowed ? ' shadowed' : ''}`,
      type: 'button',
    },
      el('div', { class: 'skill-item-header' },
        el('strong', { text: s.name }),
        ownerBadge,
      ),
      el('span', { text: s.description || 'No description' }),
      el('small', { text: `updated ${s.updated_at?.slice(0, 16).replace('T', ' ')}` }),
    );
    item.addEventListener('click', () => selectSkill(s.id, s.owned_by));
    list.append(item);
  }
}

async function selectSkill(id, ownedBy) {
  state.activeId = id;
  state.activeOwnedBy = ownedBy || '';
  state.activeFile = null;
  const { skill } = await rpc('skills.read', { id, owned_by: ownedBy || undefined });
  renderList();
  document.getElementById('skill-editor-title').textContent = skill.name;
  const ownerLabel = formatOwner(skill.owned_by);
  document.getElementById('skill-editor-meta').textContent = skill.description || '' + (ownerLabel ? ` · ${ownerLabel}` : '');
  document.getElementById('skill-editor-empty').hidden = true;
  document.getElementById('skill-file-viewer').hidden = true;
  renderSkillFiles(skill.files, skill.name);
}

function formatOwner(ownedBy) {
  if (!ownedBy) return '';
  if (ownedBy === 'user') return 'user';
  if (ownedBy === 'builtin') return 'builtin';
  if (ownedBy.startsWith('plugin:')) return ownedBy;
  return ownedBy;
}

function renderSkillFiles(files, skillName) {
  const tree = document.getElementById('skill-files-tree');
  const empty = document.getElementById('skill-tree-empty');
  const meta = document.getElementById('skill-tree-meta');
  if (!tree) return;
  tree.replaceChildren();
  const fileEntries = (files || []).filter((f) => f.type === 'file');
  if (fileEntries.length === 0) {
    tree.hidden = true;
    if (empty) empty.hidden = false;
    if (meta) meta.textContent = skillName ? `${skillName} — no files` : 'No skill selected';
    return;
  }
  tree.hidden = false;
  if (empty) empty.hidden = true;
  if (meta) meta.textContent = skillName || '';
  for (const f of fileEntries) {
    const row = el('div', {
      class: 'skill-file-row' + (state.activeFile === f.path ? ' active' : ''),
    },
      el('span', { class: 'skill-file-icon', text: '·' }),
      el('span', { class: 'skill-file-path', text: f.path, title: f.path }),
      el('span', { class: 'skill-file-size', text: formatFileSize(f.sizeBytes) }),
    );
    row.addEventListener('click', () => openSkillFile(f.path));
    tree.append(row);
  }
}

function formatFileSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

async function openSkillFile(path) {
  if (!state.activeId) return;
  state.activeFile = path;
  // Re-render tree to highlight active file.
  const skill = state.skills.find((s) => s.id === state.activeId);
  if (skill) {
    const { skill: full } = await rpc('skills.read', { id: state.activeId, owned_by: state.activeOwnedBy || undefined });
    renderSkillFiles(full.files, skill.name);
  }
  try {
    const res = await rpc('skills.file.read', { id: state.activeId, owned_by: state.activeOwnedBy || undefined, path });
    document.getElementById('skill-editor-empty').hidden = true;
    document.getElementById('skill-file-viewer').hidden = false;
    document.getElementById('skill-file-path').textContent = path;
    document.getElementById('skill-file-size').textContent = formatFileSize(res.sizeBytes) + (res.truncated ? ' (truncated)' : '');
    document.getElementById('skill-file-content').textContent = res.content || '';
  } catch (err) {
    toast(err.message, 'error');
  }
}

function showEmpty() {
  document.getElementById('skill-editor-title').textContent = 'No file selected';
  document.getElementById('skill-editor-meta').textContent = '';
  document.getElementById('skill-editor-empty').hidden = false;
  document.getElementById('skill-file-viewer').hidden = true;
  const tree = document.getElementById('skill-files-tree');
  if (tree) tree.replaceChildren();
  const empty = document.getElementById('skill-tree-empty');
  if (empty) empty.hidden = false;
  const meta = document.getElementById('skill-tree-meta');
  if (meta) meta.textContent = 'No skill selected';
  state.activeFile = null;
  state.activeOwnedBy = '';
}

// ---- Draggable splitters ----

function initSplitters() {
  const ws = document.getElementById('skills-workspace');
  if (!ws) return;
  const s1 = document.getElementById('skills-splitter-1');
  const s2 = document.getElementById('skills-splitter-2');

  const saved1 = localStorage.getItem('skills-col-1');
  const saved2 = localStorage.getItem('skills-col-2');
  if (saved1) ws.style.setProperty('--skills-col-1', saved1);
  if (saved2) ws.style.setProperty('--skills-col-2', saved2);

  if (s1) makeSplitter(s1, ws, '--skills-col-1', 150, 450);
  if (s2) makeSplitter(s2, ws, '--skills-col-2', 150, 500);
}

function makeSplitter(splitter, ws, varName, minPx, maxPx) {
  let dragging = false;

  splitter.addEventListener('mousedown', (e) => {
    e.preventDefault();
    dragging = true;
    splitter.classList.add('dragging');
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  });

  document.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const rect = ws.getBoundingClientRect();
    let px;
    if (varName === '--skills-col-1') {
      px = e.clientX - rect.left;
    } else {
      const col1 = parseFloat(getComputedStyle(ws).getPropertyValue('--skills-col-1')) || 250;
      px = e.clientX - rect.left - col1 - 8;
    }
    px = Math.max(minPx, Math.min(maxPx, px));
    const val = `${px}px`;
    ws.style.setProperty(varName, val);
    localStorage.setItem(varName.replace('--', ''), val);
  });

  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    splitter.classList.remove('dragging');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  });
}
