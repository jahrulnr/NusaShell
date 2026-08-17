// Skills workspace: catalog + editor.

import { rpc } from '../rpc.js';
import { el, toast, confirmDialog, debounce } from '../ui.js';

const state = { skills: [], activeId: null, dirty: false };

export async function initSkills() {
  document.getElementById('new-skill-btn').addEventListener('click', newSkill);
  document.getElementById('skill-save-btn').addEventListener('click', saveSkill);
  document.getElementById('skill-delete-btn').addEventListener('click', deleteSkill);
  document.getElementById('skill-run-btn').addEventListener('click', runSkill);
  document.getElementById('skills-search').addEventListener('input', debounce(renderList, 150));

  for (const id of ['skill-name', 'skill-description', 'skill-content']) {
    document.getElementById(id).addEventListener('input', () => { state.dirty = true; });
  }
  await refresh();
}

export async function refresh() {
  const { skills } = await rpc('skills.list');
  state.skills = skills;
  document.getElementById('skills-count').textContent = `${skills.length} skill${skills.length === 1 ? '' : 's'}`;
  renderList();
  if (state.activeId) {
    const still = skills.find((s) => s.id === state.activeId);
    if (!still) { state.activeId = null; showEmpty(); }
    else await selectSkill(state.activeId, false);
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
      el('span', { text: q ? 'Try a different search.' : 'Create a skill to give the agent reusable instructions.' }),
    ));
    return;
  }
  for (const s of filtered) {
    const item = el('button', {
      class: `skills-list-item${s.id === state.activeId ? ' active' : ''}`,
      type: 'button',
    },
      el('strong', { text: s.name }),
      el('span', { text: s.description || 'No description' }),
      el('small', { text: `updated ${s.updated_at?.slice(0, 16).replace('T', ' ')}` }),
    );
    item.addEventListener('click', () => selectSkill(s.id));
    list.append(item);
  }
}

async function selectSkill(id, reload = true) {
  if (state.dirty && reload) {
    const ok = await confirmDialog('Discard changes?', 'You have unsaved edits. Discard them and switch skills?', 'Discard', true);
    if (!ok) return;
    state.dirty = false;
  }
  state.activeId = id;
  const { skill } = await rpc('skills.read', { id });
  renderList();
  document.getElementById('skill-editor-title').textContent = skill.name;
  document.getElementById('skill-editor-meta').textContent = skill.description || '';
  document.getElementById('skill-name').value = skill.name;
  document.getElementById('skill-description').value = skill.description || '';
  document.getElementById('skill-content').value = skill.content || '';
  document.getElementById('skill-editor-form').hidden = false;
  document.getElementById('skill-editor-empty').hidden = true;
  document.getElementById('skill-save-btn').hidden = false;
  document.getElementById('skill-delete-btn').hidden = false;
  document.getElementById('skill-run-btn').hidden = false;
  renderSkillFiles(skill.files);
  state.dirty = false;
}

function renderSkillFiles(files) {
  const panel = document.getElementById('skill-files');
  const tree = document.getElementById('skill-files-tree');
  if (!panel || !tree) return;
  if (!Array.isArray(files) || files.length === 0) {
    panel.hidden = true;
    tree.replaceChildren();
    return;
  }
  panel.hidden = false;
  tree.replaceChildren();
  for (const f of files) {
    const row = el('div', { class: 'skill-file-row' + (f.type === 'directory' ? ' is-dir' : '') },
      el('span', { class: 'skill-file-icon', text: f.type === 'directory' ? '▸' : '·' }),
      el('span', { class: 'skill-file-path', text: f.path }),
      f.type === 'file' ? el('span', { class: 'skill-file-size', text: `${f.sizeBytes} B` }) : null,
    );
    tree.append(row);
  }
}

function showEmpty() {
  const panel = document.getElementById('skill-files');
  if (panel) panel.hidden = true;
  document.getElementById('skill-editor-title').textContent = 'No skill selected';
  document.getElementById('skill-editor-meta').textContent = '';
  document.getElementById('skill-editor-form').hidden = true;
  document.getElementById('skill-editor-empty').hidden = false;
  document.getElementById('skill-save-btn').hidden = true;
  document.getElementById('skill-delete-btn').hidden = true;
  document.getElementById('skill-run-btn').hidden = true;
}

async function newSkill() {
  if (state.dirty) {
    const ok = await confirmDialog('Discard changes?', 'You have unsaved edits. Discard them and start a new skill?', 'Discard', true);
    if (!ok) return;
  }
  state.activeId = null;
  state.dirty = false;
  renderList();
  document.getElementById('skill-editor-title').textContent = 'New skill';
  document.getElementById('skill-editor-meta').textContent = 'not saved yet';
  document.getElementById('skill-name').value = '';
  document.getElementById('skill-description').value = '';
  document.getElementById('skill-content').value = '';
  document.getElementById('skill-editor-form').hidden = false;
  document.getElementById('skill-editor-empty').hidden = true;
  document.getElementById('skill-save-btn').hidden = false;
  document.getElementById('skill-delete-btn').hidden = true;
  document.getElementById('skill-run-btn').hidden = true;
  document.getElementById('skill-name').focus();
}

async function saveSkill() {
  const name = document.getElementById('skill-name').value.trim();
  const description = document.getElementById('skill-description').value.trim();
  const content = document.getElementById('skill-content').value;
  if (!name) { toast('Skill name is required', 'error'); return; }
  if (!content.trim()) { toast('Skill content is required', 'error'); return; }
  try {
    const { skill } = await rpc('skills.save', {
      id: state.activeId || undefined,
      name,
      description,
      content,
    });
    state.activeId = skill.id;
    state.dirty = false;
    toast('Skill saved', 'success');
    await refresh();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function deleteSkill() {
  const ok = await confirmDialog('Delete skill', `"${document.getElementById('skill-name').value}" will be removed from the library.`, 'Delete');
  if (!ok) return;
  try {
    await rpc('skills.delete', { id: state.activeId });
    state.activeId = null;
    toast('Skill deleted', 'success');
    await refresh();
    showEmpty();
  } catch (err) {
    toast(err.message, 'error');
  }
}

async function runSkill() {
  try {
    const { conversation_id } = await rpc('skills.run', { id: state.activeId });
    toast('Skill opened in a new conversation', 'success');
    // navigate to the agent view; it will pick up the new conversation on next refresh
    document.querySelector('[data-nav][data-view="agent"]').click();
    await new Promise((r) => setTimeout(r, 150));
    const { conversations } = await rpc('agent.conversations.list');
    const conv = conversations.find((c) => c.id === conversation_id);
    if (conv) {
      const evt = new CustomEvent('nusashell:open-conversation', { detail: conv.id });
      document.dispatchEvent(evt);
    }
  } catch (err) {
    toast(err.message, 'error');
  }
}
