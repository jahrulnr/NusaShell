export const supportedEventTypes = Object.freeze([
  'user.message',
  'agent.reasoning',
  'tool.run',
  'agent.message',
]);

const tags = Object.freeze({
  'user.message': 'user-message-widget',
  'agent.reasoning': 'reasoning-widget',
  'agent.message': 'agent-message-widget',
});

export function eventWidgetTag(type, event = null) {
  if (type === 'tool.run') return toolWidgetDefinition(event?.tool, event?.presentation).tag;
  return tags[type] || 'unknown-event-widget';
}
import { toolWidgetDefinition } from './tools/registry.js';

