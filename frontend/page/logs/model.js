const LEVEL_LABELS = Object.freeze({
  debug: 'Debug',
  info: 'Info',
  warn: 'Warning',
  error: 'Error',
});

export function normalizeLogLevel(value) {
  const level = String(value || '').trim().toLowerCase();
  return LEVEL_LABELS[level] ? level : 'other';
}

export function logLevelLabel(value) {
  const level = String(value || '').trim().toLowerCase();
  return LEVEL_LABELS[level] || (level ? level[0].toUpperCase() + level.slice(1) : 'Other');
}

export function filterLogEntries(entries = [], query = '', level = '') {
  const normalizedQuery = String(query || '').trim().toLocaleLowerCase();
  const normalizedLevel = String(level || '').trim().toLowerCase();
  return (Array.isArray(entries) ? entries : []).filter((entry) => {
    if (normalizedLevel && String(entry?.level || '').toLowerCase() !== normalizedLevel) return false;
    if (!normalizedQuery) return true;
    return [entry?.id, entry?.time, entry?.level, entry?.source, entry?.message]
      .filter(Boolean)
      .join(' ')
      .toLocaleLowerCase()
      .includes(normalizedQuery);
  });
}

export function normalizeLogEntry(entry) {
  if (!entry?.id) return null;
  return {
    id: String(entry.id),
    time: String(entry.time || ''),
    level: String(entry.level || 'info').trim().toLowerCase() || 'info',
    source: String(entry.source || 'runtime'),
    message: String(entry.message || ''),
  };
}

