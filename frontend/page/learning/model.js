const DOCUMENT_TIERS = new Set(['user', 'agent']);

export function isMemoryRecord(entry) {
  if (!entry) return false;
  const tier = String(entry.tier || '').trim().toLowerCase();
  if (DOCUMENT_TIERS.has(tier)) return false;
  return tier === 'record'
    || tier === 'records'
    || entry.body != null
    || entry.type != null
    || entry.subject != null
    || entry.predicate != null
    || entry.object != null;
}

export function memoryEntryBody(entry) {
  if (!entry) return '';
  const structured = [entry.subject, entry.predicate, entry.object]
    .filter((value) => String(value || '').trim())
    .join(' ');
  return String(entry.body || structured || entry.content || '').trim();
}

export function filterMemoryEntries(entries = [], query = '') {
  const needle = String(query || '').trim().toLowerCase();
  const records = entries.filter(isMemoryRecord);
  if (!needle) return records.slice();
  const tokens = needle.split(/\s+/).filter(Boolean);
  return records.filter((entry) => {
    const haystack = [
      memoryEntryBody(entry), entry.type, entry.status, entry.project,
      entry.scope?.level, entry.scope?.project, entry.source,
    ].join(' ').toLowerCase();
    return tokens.every((token) => haystack.includes(token));
  });
}

export function summarizeSignals(signals = {}) {
  const result = [];
  const count = (key, singular, plural = `${singular}s`) => {
    const value = Number(signals?.[key]) || 0;
    if (value > 0) result.push(`${value} ${value === 1 ? singular : plural}`);
  };
  count('user_corrections', 'correction', 'corrections');
  count('retries', 'retry', 'retries');
  count('failed_actions', 'failed action', 'failed actions');
  if (signals?.verified_success) result.push('verified');
  if (signals?.novel_workflow) result.push('new workflow');
  if (signals?.explicit_teaching) result.push('teaching');
  if (signals?.root_cause_recovered) result.push('recovered');
  return result;
}

export function formatLearningDate(value) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return 'Unknown time';
  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }
  return date.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' });
}

export function oneLine(value, maxLength = 120) {
  const text = String(value || '').replace(/\s+/g, ' ').trim();
  return text.length > maxLength ? `${text.slice(0, maxLength - 1)}…` : text;
}

export function outcomeLabel(experience) {
  const status = typeof experience?.outcome === 'string'
    ? experience.outcome
    : experience?.outcome?.status;
  return String(status || 'unknown').trim().toLowerCase() || 'unknown';
}

export function learningTypeLabel(type) {
  const labels = {
    review: 'Learning job',
    extract: 'Extraction',
    edge_build: 'Edge build',
    consolidate: 'Consolidation',
    skill_evolve: 'Skill evolved',
    skill_promote: 'Skill promoted',
    decay: 'Decay',
    prune: 'Prune',
    review_model: 'Learning model',
  };
  return labels[type] || String(type || 'Activity').replaceAll('_', ' ');
}

export function sortExperiencesNewestFirst(entries = []) {
  const at = (entry) => {
    const value = entry?.timestamp || entry?.created_at;
    const time = Date.parse(value || '');
    return Number.isNaN(time) ? 0 : time;
  };
  return (entries || []).slice().sort((left, right) => at(right) - at(left));
}

export function memoryTierLabel(tier) {
  const labels = { user: 'About you', agent: 'About agent', record: 'Record' };
  return labels[String(tier || '').toLowerCase()] || 'Memory';
}
