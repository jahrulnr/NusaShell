const FILTER_GROUPS = Object.freeze({
  message: new Set(['user.message', 'agent.reasoning', 'agent.message']),
  activity: new Set(['tool.run']),
});

export function filterEvents(events, filter) {
  if (filter === 'all') return events;
  const types = FILTER_GROUPS[filter];
  return types ? events.filter((event) => types.has(event.type)) : events;
}

// todoStatus keeps the dock tolerant of both the current Todo DTO and the
// older task-plan shape used by the prototype fixture. Unknown values are
// deliberately treated as pending so work is never presented as complete.
export function todoStatus(item = {}) {
  const raw = String(item.status || '').trim().toLowerCase().replace(/[\s-]+/g, '_');
  if (raw === 'completed' || raw === 'complete' || raw === 'done') return 'completed';
  if (raw === 'in_progress' || raw === 'running' || raw === 'active') return 'in_progress';
  if (item.done === true) return 'completed';
  return 'pending';
}

export function normalizeTodoItem(item = {}) {
  const status = todoStatus(item);
  return {
    ...item,
    id: String(item.id ?? ''),
    label: String(item.label ?? item.content ?? ''),
    status,
    done: status === 'completed',
  };
}

export function summarizeTodos(items = []) {
  return items.reduce((summary, item) => {
    const status = todoStatus(item);
    summary.total += 1;
    summary[status] += 1;
    return summary;
  }, { total: 0, pending: 0, in_progress: 0, completed: 0 });
}

export function toggleTask(tasks, id) {
  return tasks.map((task) => {
    if (task.id !== id) return task;
    const completed = todoStatus(task) === 'completed';
    const next = completed ? 'pending' : 'completed';
    const updated = { ...task, done: !completed };
    if (task.status) updated.status = next;
    return updated;
  });
}
