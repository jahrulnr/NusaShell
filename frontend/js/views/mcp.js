// MCP workspace: stdio server management.

import { rpc } from '../rpc.js';
import { el, toast, dialog, confirmDialog } from '../ui.js';

let servers = [];

export async function initMcp() {
  document.getElementById('add-mcp-btn').addEventListener('click', () => addMcp());
  await refresh();
}

async function refresh() {
  const res = await rpc('mcp.servers.list');
  servers = res.servers ?? [];
  renderList();
}

function renderList() {
  const list = document.getElementById('mcp-list');
  list.innerHTML = '';
  if (!servers.length) {
    list.append(el('div', { class: 'empty-state' },
      el('div', { class: 'empty-mark', text: '🔌' }),
      el('strong', { text: 'No MCP servers yet' }),
      el('span', { text: 'Add a stdio MCP server (e.g. npx, python, or a local binary) to expose its tools to the agent.' }),
    ));
    return;
  }
  for (const s of servers) {
    const row = el('div', { class: 'mcp-row' },
      el('div', { class: 'mcp-row-icon', text: s.name.slice(0, 2).toUpperCase() }),
      el('div', { class: 'mcp-row-info' },
        el('div', { class: 'mcp-row-name', text: s.name }),
        el('div', { class: 'mcp-row-meta', text: `${s.command} ${(s.args || []).join(' ')}${s.enabled === false ? ' · disabled' : ''}` }),
        s.tools?.length ? el('div', { class: 'mcp-row-tools' }, s.tools.slice(0, 12).map((t) => el('span', { class: 'mcp-tool-chip', text: t.name }))) : null,
      ),
      el('span', { class: `mcp-row-state ${s.status === 'connected' ? 'connected' : s.status === 'error' ? 'error' : 'idle'}`, text: s.status || 'idle' }),
      el('div', { class: 'mcp-row-actions' },
        el('label', { class: 'toggle', title: s.enabled === false ? 'Server is disabled' : 'Server is enabled' },
          el('input', { type: 'checkbox', checked: s.enabled !== false }),
          el('span', { class: 'toggle-slider' }),
        ),
        el('button', { class: 'mini-btn ghost', type: 'button', text: 'Test' }),
        el('button', { class: 'mini-btn ghost', type: 'button', text: 'Edit' }),
        el('button', { class: 'mini-btn danger', type: 'button', text: 'Delete' }),
      ),
    );
    const [toggle, testBtn, editBtn, delBtn] = row.querySelectorAll('button, input');
    toggle.addEventListener('change', async () => {
      try {
        await rpc('mcp.servers.save', { id: s.id, name: s.name, command: s.command, args: s.args, env: s.env, enabled: toggle.checked });
        toast(toggle.checked ? 'MCP server enabled' : 'MCP server disabled', 'success');
        await refresh();
      } catch (err) { toast(err.message, 'error'); }
    });
    testBtn.addEventListener('click', async () => {
      testBtn.disabled = true;
      testBtn.textContent = 'Testing…';
      try {
        const res = await rpc('mcp.servers.test', { id: s.id });
        toast(`Connected · ${res.tools?.length ?? 0} tools`, 'success');
      } catch (err) {
        toast(err.message, 'error');
      } finally {
        testBtn.disabled = false;
        testBtn.textContent = 'Test';
        await refresh();
      }
    });
    editBtn.addEventListener('click', () => addMcp(s));
    delBtn.addEventListener('click', async () => {
      const ok = await confirmDialog('Delete MCP server', `"${s.name}" will be removed. The subprocess is not started until needed.`, 'Delete');
      if (!ok) return;
      try {
        await rpc('mcp.servers.delete', { id: s.id });
        toast('MCP server deleted', 'success');
        await refresh();
      } catch (err) { toast(err.message, 'error'); }
    });
    list.append(row);
  }
}

async function addMcp(server = null) {
  const res = await dialog({
    title: server ? 'Edit MCP server' : 'Add MCP server',
    message: 'A stdio MCP server launched as a child process. Environment entries use KEY=VALUE.',
    fields: [
      { name: 'name', label: 'Name', value: server?.name ?? '', placeholder: 'e.g. filesystem' },
      { name: 'command', label: 'Command', value: server?.command ?? '', placeholder: 'e.g. npx' },
      { name: 'args', label: 'Arguments (space separated)', value: (server?.args ?? []).join(' '), placeholder: '-y @modelcontextprotocol/server-filesystem /path' },
      { name: 'env', label: 'Environment (optional)', value: Object.entries(server?.env ?? {}).map(([k, v]) => `${k}=${v}`).join('\n'), placeholder: 'KEY=VALUE', tag: 'textarea' },
    ],
    actions: [
      { label: 'Cancel', value: null },
      { label: 'Save', value: 'save' },
    ],
  });
  if (res.value !== 'save') return;
  const { name, command, args, env } = res.fields;
  if (!name.trim() || !command.trim()) { toast('Name and command are required', 'error'); return; }
  const envObj = {};
  for (const line of env.split('\n')) {
    const i = line.indexOf('=');
    if (i > 0) envObj[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  try {
    await rpc('mcp.servers.save', {
      id: server?.id || undefined,
      name: name.trim(),
      command: command.trim(),
      args: args.split(/\s+/).filter(Boolean),
      env: envObj,
      enabled: server?.enabled !== false,
    });
    toast('MCP server saved', 'success');
    await refresh();
  } catch (err) {
    toast(err.message, 'error');
  }
}
