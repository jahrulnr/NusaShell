// What a provider speaks, named once for every form that offers it: the
// first-run wizard, the provider dialog, and a model pinned to another
// protocol. The server's own style ids are the values.
export const API_STYLES = [
  { value: 'completion', text: 'Chat Completions (OpenAI-compatible)' },
  { value: 'responses', text: 'Responses (OpenAI)' },
  { value: 'messages', text: 'Messages (Anthropic)' },
  { value: 'generatecontent', text: 'generateContent (Gemini)' },
];

// STYLE_SHORT names a style where the long label does not fit (a badge).
export const STYLE_SHORT = Object.fromEntries(API_STYLES.map((s) => [s.value, s.text.replace(/ \(.*\)$/, '')]));
