// HTTP client for the loopback API. Every failure becomes an ApiError with
// the server's problem+json `code`/`detail`, or a client-side code for
// network failure and timeout, so callers never parse provider text.

// The server sets no HTTP write timeout; upstream calls are bounded by
// service.header_timeout (30s default) plus streaming idle time. Plain
// reads/writes answer from local storage, so 30s is a generous ceiling.
// Calls that reach a provider (provider save/import, compaction) get the
// long budget instead of failing before the backend does.
export const TIMEOUT_MS = 30_000;
export const LONG_TIMEOUT_MS = 10 * 60_000;

export class ApiError extends Error {
  constructor(status, code, message, problem = false) {
    super(message);
    this.status = status;
    this.code = code;
    // problem is true when the body was our own problem document (it carried
    // a code): the status is then the server's answer, not a gateway's.
    this.problem = problem;
  }
}

// The session's CSRF token, echoed on every state-changing request when
// the server requires a login. The session cookie itself is HttpOnly.
let csrfToken = '';
export function setCsrf(token) { csrfToken = token || ''; }
// onUnauthenticated runs when the server says the session is gone.
let unauthenticated = () => {};
export function onUnauthenticated(fn) { unauthenticated = fn; }

async function request(method, path, { body, timeout = TIMEOUT_MS, signal, keepalive = false } = {}) {
  const ctl = new AbortController();
  const timer = setTimeout(() => ctl.abort(new DOMException('timeout', 'TimeoutError')), timeout);
  const onAbort = () => ctl.abort(signal.reason);
  signal?.addEventListener('abort', onAbort, { once: true });
  let res;
  try {
    res = await fetch(path, {
      method,
      signal: ctl.signal,
      keepalive,
      headers: {
        ...(body === undefined || body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
        ...(method !== 'GET' && csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
      },
      body: body === undefined || body instanceof FormData ? body : JSON.stringify(body),
    });
  } catch (err) {
    if (signal?.aborted) throw err;
    if (ctl.signal.aborted) throw new ApiError(0, 'timeout', 'The server took too long to answer.');
    throw new ApiError(0, 'network', 'Cannot reach the NusaShell server. Is `nusashell serve` running?');
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', onAbort);
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* non-JSON */ }
  if (res.status === 401 && data?.code === 'unauthenticated' && !path.startsWith('/api/auth/')) unauthenticated();
  if (!res.ok) {
    throw new ApiError(res.status, data?.code || 'http_' + res.status, data?.detail || data?.title || `Request failed (HTTP ${res.status})`, Boolean(data?.code));
  }
  return data;
}

const q = (params) => {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') s.set(k, v);
  const str = s.toString();
  return str ? '?' + str : '';
};
const id = encodeURIComponent;

// fileURL serves a local file a thread links to (a path, ~ path, or
// file: URL; relative paths resolve in the thread's workspace).
export const fileURL = (cid, path, download = false) =>
  `/api/conversations/${id(cid)}/files` + q({ path, download: download ? 1 : undefined });

export const api = {
  authStatus: () => request('GET', '/api/auth/status'),
  login: (password) => request('POST', '/api/auth/login', { body: { password } }),
  logout: () => request('POST', '/api/auth/logout'),
  changePassword: (current, next) => request('POST', '/api/owner/password', { body: { current, new: next } }),

  conversations: (offset = 0, limit = 30, projectId) => request('GET', '/api/conversations' + q({ offset, limit, projectId })),
  conversation: (cid) => request('GET', `/api/conversations/${id(cid)}`),
  messages: (cid, start, limit = 60) => request('GET', `/api/conversations/${id(cid)}/messages` + q({ start, limit })),
  // annotate sets any of { title, projectId ('' leaves the project), pinned }.
  annotate: (cid, patch) => request('PATCH', `/api/conversations/${id(cid)}`, { body: patch }),
  // A delete waits for the room's running turn to stop, and an agent may take
  // a while to answer the stop, so it gets longer than a plain call.
  remove: (cid) => request('DELETE', `/api/conversations/${id(cid)}`, { timeout: 2 * 60_000 }),
  // A purge can run a while — every thread, its uploads, picks,
  // recordings, and views. The confirm word is the contract's own check.
  deleteAllConversations: () => request('DELETE', '/api/conversations', { body: { confirm: 'DELETE' }, timeout: LONG_TIMEOUT_MS }),
  // commands is the room's slash-command catalog: the native commands plus the
  // room's agent commands, native first, each with this room's availability.
  // Without a conversationId it answers the native set, every row off.
  commands: (params) => request('GET', '/api/commands' + q(params || {}), { timeout: 20_000 }),
  // runCommand runs one native command — an action, not a turn. Compaction can
  // fold a lot of history, so it gets the long budget.
  runCommand: (cid, body) => request('POST', `/api/conversations/${id(cid)}/commands`, { body, timeout: LONG_TIMEOUT_MS }),
  // The cross-thread activity log — which thread called which
  // conversation tool. Metadata only; ?caller=/target=/limit= filter it.
  controlActivity: (params) => request('GET', '/api/conversation-control/activity' + q(params)),
  context: (params) => request('GET', '/api/context' + q(params)),

  providers: () => request('GET', '/api/providers?limit=100'),
  provider: (pid) => request('GET', `/api/providers/${id(pid)}`),
  saveProvider: (pid, doc) => request('PUT', `/api/providers/${id(pid)}`, { body: doc, timeout: LONG_TIMEOUT_MS }),
  importModels: (pid) => request('POST', `/api/providers/${id(pid)}/import-models`, { timeout: LONG_TIMEOUT_MS }),
  deleteProvider: (pid) => request('DELETE', `/api/providers/${id(pid)}`),
  upstreams: (pid, model) => request('GET', `/api/providers/${id(pid)}/upstreams` + q({ model })),

  projects: () => request('GET', '/api/projects'),
  createProject: (doc) => request('POST', '/api/projects', { body: doc }),
  updateProject: (pid, doc) => request('PUT', `/api/projects/${id(pid)}`, { body: doc }),
  pinProject: (pid, pinned) => request('PATCH', `/api/projects/${id(pid)}`, { body: { pinned } }),
  deleteProject: (pid) => request('DELETE', `/api/projects/${id(pid)}`),
  restoreProject: (pid, doc) => request('POST', `/api/projects/${id(pid)}/restore`, { body: doc }),

  channels: () => request('GET', '/api/channels'),
  channelSettings: (name) => request('GET', `/api/channels/${id(name)}/settings`),
  saveChannelSettings: (name, doc) => request('PUT', `/api/channels/${id(name)}/settings`, { body: doc }),
  channelAction: (name, action) => request('POST', `/api/channels/${id(name)}/${action}`, { timeout: 60_000 }),
  channelGroups: (name) => request('GET', `/api/channels/${id(name)}/groups`),
  channelQR: (name) => `/api/channels/${id(name)}/qr`,
  chats: () => request('GET', '/api/chats'),
  memories: (f) => request('GET', '/api/memories' + q(f || {})),
  createMemory: (doc) => request('POST', '/api/memories', { body: doc }),
  updateMemory: (mid, doc) => request('PUT', `/api/memories/${id(mid)}`, { body: doc }),
  memoryAction: (mid, action, reason) => request('POST', `/api/memories/${id(mid)}/actions`, { body: { action, reason } }),
  deleteMemory: (mid) => request('DELETE', `/api/memories/${id(mid)}`),
  memorySuggestions: (f) => request('GET', '/api/memory-suggestions' + q(f || {})),
  decideMemorySuggestion: (sid, action, edit) => request('POST', `/api/memory-suggestions/${id(sid)}/actions`, { body: { action, edit } }),
  // skills reads the Skills page for one workspace root (none: the owner's
  // roots only). Not paged: the server bounds the catalog by its byte budget.
  skills: (workspace = '') => request('GET', '/api/skills' + q({ workspace })),
  // moveSkillLevel records one owner decision: the level the skill moves to and
  // the digest the page read. A file that changed since answers 409 (an ApiError
  // with status 409); the caller must read the row's digest again before it
  // retries, because the same digest sent again answers 409 again.
  moveSkillLevel: (name, { workspace = '', level, digest }) =>
    request('POST', `/api/skills/${id(name)}/level` + q({ workspace }), { body: { level, digest } }),
  // skillProposals is the queue above the Skills list. Reading writes nothing.
  // decideSkillProposal records one decision. Dismiss sends the owner's reason;
  // this page does not send accept, which needs a body the wire does not give.
  skillProposals: (workspace = '') => request('GET', '/api/skill-proposals' + q({ workspace })),
  decideSkillProposal: (pid, { workspace = '', action, reason }) =>
    request('POST', `/api/skill-proposals/${id(pid)}/decide` + q({ workspace }), { body: { action, reason } }),
  webSearch: () => request('GET', '/api/websearch'),
  updateWebSearch: (values) => request('PATCH', '/api/websearch', { body: values }),
  setWebSearchKey: (source, value) => request('PUT', `/api/websearch/keys/${id(source)}`, { body: { value } }),
  clearWebSearchKey: (source) => request('DELETE', `/api/websearch/keys/${id(source)}`),
  // A search waits on its slowest source, up to the timeout (2 minutes at most).
  testWebSearch: (body) => request('POST', '/api/websearch/test', { body, timeout: 3 * 60_000 }),
  automations: () => request('GET', '/api/automations'),
  createAutomation: (doc) => request('POST', '/api/automations', { body: doc }),
  updateAutomation: (aid, doc) => request('PUT', `/api/automations/${id(aid)}`, { body: doc }),
  enableAutomation: (aid, enabled) => request('PATCH', `/api/automations/${id(aid)}`, { body: { enabled } }),
  deleteAutomation: (aid) => request('DELETE', `/api/automations/${id(aid)}`),
  automationRuns: (aid) => request('GET', `/api/automations/${id(aid)}/runs`),
  runAutomation: (aid) => request('POST', `/api/automations/${id(aid)}/runs`),
  testAutomation: (aid) => request('POST', `/api/automations/${id(aid)}/test`),
  notifications: () => request('GET', '/api/notifications'),
  testNotification: () => request('POST', '/api/notifications/test'),
  links: (cid) => request('GET', `/api/conversations/${id(cid)}/links`),
  agentOptions: (cid) => request('GET', `/api/conversations/${id(cid)}/agent-options`),
  // agentOptionsFor reads the agent's own session options before a thread
  // exists — the modes and config it reports for a fresh session.
  agentOptionsFor: (aid) => request('GET', `/api/agents/${id(aid)}/options`, { timeout: 2 * 60_000 }),
  setAgentOption: (cid, oid, value) => request('PUT', `/api/conversations/${id(cid)}/agent-options`, { body: { id: oid, value } }),
  approvalRules: (cid) => request('GET', `/api/conversations/${id(cid)}/approval-rules`),
  deleteApprovalRule: (cid, rid) => request('DELETE', `/api/conversations/${id(cid)}/approval-rules/${id(rid)}`),
  upload: (file, signal) => {
    const form = new FormData();
    form.append('file', file, file.name || 'upload');
    return request('POST', '/api/uploads', { body: form, timeout: LONG_TIMEOUT_MS, signal });
  },
  // transcribe turns a voice recording into text on the speech-to-text role.
  transcribe: (audio, signal) => {
    const form = new FormData();
    form.append('audio', audio, 'voice.' + ((audio.type.split(';')[0].split('/')[1]) || 'webm'));
    return request('POST', '/api/transcriptions', { body: form, timeout: LONG_TIMEOUT_MS, signal });
  },
  references: (params, signal) => request('GET', `/api/references?${new URLSearchParams(params)}`, { signal, timeout: 20_000 }),
  // view is a thread's kept draft and side pane; no id is the composer of
  // a thread not started yet. keepalive lets a save outlive the page.
  view: (cid) => request('GET', cid ? `/api/conversations/${id(cid)}/view` : '/api/view'),
  saveView: (cid, document, keepalive = false) =>
    request('PUT', cid ? `/api/conversations/${id(cid)}/view` : '/api/view', { body: { document }, keepalive }),
  roots: (params) => request('GET', `/api/roots?${new URLSearchParams(params)}`),
  browser: (cid) => request('GET', `/api/conversations/${id(cid)}/browser`),
  // Starting the browser launches Chrome, which takes a moment.
  startBrowser: (cid, url) => request('POST', `/api/conversations/${id(cid)}/browser`, { body: { url }, timeout: 60_000 }),
  closeBrowser: (cid) => request('DELETE', `/api/conversations/${id(cid)}/browser`),
  browserProfiles: () => request('GET', '/api/browser/profiles'),
  browserInstallOffer: () => request('GET', '/api/browser/install'),
  // The server gives an install 15 minutes.
  browserInstall: (method) => request('POST', '/api/browser/install', { body: { method }, timeout: 16 * 60_000 }),
  recordings: (cid) => request('GET', `/api/conversations/${id(cid)}/recordings`),
  recordingSummaryURL: (cid, rid) => `/api/conversations/${id(cid)}/recordings/${id(rid)}/summary`,
  pageLogins: (cid) => request('GET', `/api/conversations/${id(cid)}/browser/logins`),
  // annotatePage is the live-view element pick. It is deliberately not
  // named `annotate`: the conversation metadata write above already owns
  // that key, and a second `annotate` in this object silently replaced it —
  // rename, pin, project, label and routing then all posted to
  // /browser/annotate and failed with "the thread's browser is not open".
  annotatePage: (cid, body) => request('POST', `/api/conversations/${id(cid)}/browser/annotate`, { body, timeout: 30000 }),
  previewAnnotate: (cid, x, y) => request('POST', `/api/conversations/${id(cid)}/browser/annotate/preview`, { body: { x, y } }),
  clearAnnotate: (cid) => request('POST', `/api/conversations/${id(cid)}/browser/annotate/clear`, { body: {} }),
  browserLogins: (profile) => request('GET', '/api/browser/logins' + q({ profile })),
  // The open page's console, and running the owner's JavaScript in it.
  browserConsole: (cid) => request('GET', `/api/conversations/${id(cid)}/browser/console`),
  browserEval: (cid, expression) => request('POST', `/api/conversations/${id(cid)}/browser/eval`, { body: { expression }, timeout: 30000 }),
  // Pages a profile visited; no profile is the default one.
  browserHistory: (profile, query) => request('GET', '/api/browser/history' + q({ profile, q: query })),
  deleteBrowserLogin: (lid) => request('DELETE', `/api/browser/logins/${id(lid)}`),
  unlink: (cid, key) => request('DELETE', `/api/conversations/${id(cid)}/links` + q({ chat: key })),

  agents: () => request('GET', '/api/agents'),
  agent: (aid) => request('GET', `/api/agents/${id(aid)}`),
  saveAgent: (aid, doc) => request('PUT', `/api/agents/${id(aid)}`, { body: doc }),
  deleteAgent: (aid) => request('DELETE', `/api/agents/${id(aid)}`),
  detectAgents: () => request('GET', '/api/agents/detect'),
  importAgents: () => request('POST', '/api/agents/import'),
  // Listing models starts the agent on first use, which can be slow.
  agentStatus: (aid) => request('GET', `/api/agents/${id(aid)}/status`),
  // Signing in may start the agent and wait on a browser flow.
  authenticateAgent: (aid, methodId) => request('POST', `/api/agents/${id(aid)}/authenticate`, { body: { methodId }, timeout: 10 * 60_000 }),
  logoutAgent: (aid) => request('POST', `/api/agents/${id(aid)}/logout`, { timeout: 60_000 }),
  restartAgent: (aid) => request('POST', `/api/agents/${id(aid)}/restart`),
  agentModels: (aid) => request('GET', `/api/agents/${id(aid)}/models`, { timeout: 2 * 60_000 }),
  answerElicitation: (cid, eid, body) => request('POST', `/api/conversations/${id(cid)}/elicitations/${id(eid)}`, { body }),
  approvals: (cid) => request('GET', `/api/conversations/${id(cid)}/approvals`),
  answerApproval: (cid, rid, optionId) => request('POST', `/api/conversations/${id(cid)}/approvals/${id(rid)}`, { body: { optionId } }),

  settings: () => request('GET', '/api/settings'),
  setSetting: (key, value) => request('PATCH', '/api/settings', { body: { values: { [key]: value } } }),
  // setSettings writes several keys as one change: all land or none do.
  setSettings: (values) => request('PATCH', '/api/settings', { body: { values } }),
  tools: (conversationId) => request('GET', '/api/tools' + q({ conversationId })),
  mcpServers: () => request('GET', '/api/mcp-servers'),
  createMcpServer: (body) => request('POST', '/api/mcp-servers', { body }),
  updateMcpServer: (sid, body) => request('PUT', `/api/mcp-servers/${id(sid)}`, { body }),
  enableMcpServer: (sid, enabled) => request('PATCH', `/api/mcp-servers/${id(sid)}`, { body: { enabled } }),
  deleteMcpServer: (sid) => request('DELETE', `/api/mcp-servers/${id(sid)}`),
  browse: (path) => request('GET', '/api/fs/browse' + q({ path })),
  list: (path) => request('GET', '/api/fs/list' + q({ path })),

  update: () => request('GET', '/api/update'),
  checkUpdate: () => request('POST', '/api/update/check', { timeout: 60_000 }),
  installUpdate: () => request('POST', '/api/update/install'),
  installProgress: () => request('GET', '/api/update/install'),
};

export function errorText(err) {
  if (err instanceof ApiError) return err.message;
  return err?.message || String(err);
}
