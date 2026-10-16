const GROUPS = Object.freeze([
  { tag: 'terminal-tool-widget', kind: 'terminal', names: ['exec', 'mcp_call'] },
  { tag: 'question-tool-widget', kind: 'question', names: ['ask_question'] },
  { tag: 'file-tool-widget', kind: 'file', names: ['file_read', 'file_write', 'file_patch', 'file_list', 'file_mkdir', 'file_delete', 'file_move', 'file_copy', 'file_info'] },
  { tag: 'search-tool-widget', kind: 'search', names: ['grep', 'find_file', 'file_search'] },
  { tag: 'web-tool-widget', kind: 'web', names: ['web_search', 'web_fetch', 'web_answer'] },
  { tag: 'media-tool-widget', kind: 'media', names: ['show', 'read_media', 'generate_media', 'generate_image', 'generate_speech', 'generate_video'] },
  { tag: 'task-tool-widget', kind: 'task', names: ['todo'] },
  { tag: 'subagent-tool-widget', kind: 'subagent', names: ['subagent', 'delegate', 'subagent_steer', 'subagent_stop', 'subagent_wait', 'subagent_result', 'delegate_result'] },
  { tag: 'automation-tool-widget', kind: 'automation', names: ['automation', 'automation_schedule', 'wait_until', 'sleep'] },
  { tag: 'knowledge-tool-widget', kind: 'knowledge', names: ['skill', 'memory', 'docs', 'memory_project', 'conversation', 'learn', 'summary'] },
  { tag: 'connector-tool-widget', kind: 'connector', names: ['mcp_list', 'tool_list', 'tool_schema', 'mcp_search', 'contract_read', 'mcp_register', 'mcp_enable', 'mcp_disable', 'mcp_unregister', 'mcp_install', 'mcp_server_add'] },
  { tag: 'generic-tool-widget', kind: 'generic', names: ['model_override'] },
]);

const BY_NAME = new Map();
for (const group of GROUPS) {
  for (const name of group.names) BY_NAME.set(name, { tag: group.tag, kind: group.kind, name });
}

const VARIANT_TAGS = Object.freeze({
  terminal: 'terminal-tool-widget',
  'file-list': 'file-tool-widget',
  'file-content': 'file-tool-widget',
  'search-results': 'search-tool-widget',
  collection: 'knowledge-tool-widget',
  document: 'knowledge-tool-widget',
  media: 'media-tool-widget',
  status: 'generic-tool-widget',
});

export const BUILTIN_TOOL_WIDGETS = Object.freeze([...BY_NAME.values()]);

export function toolWidgetDefinition(name, presentation = null) {
  const normalized = String(name || 'tool');
  const known = BY_NAME.get(normalized);
  if (known) return known;
  const tag = VARIANT_TAGS[presentation?.variant] || 'generic-tool-widget';
  return { name: normalized, tag, kind: tag.replace(/-tool-widget$/, '') };
}
