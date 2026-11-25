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

// UNUSED_MODEL_TYPES are the catalog types no role consumes yet — a model
// tagged with one is saved but stays out of every picker. The value itself
// remains valid; the note on the option says why it will not appear.
export const UNUSED_MODEL_TYPES = new Set(['audio', 'decision', 'realtime', 'rerank']);

// MODEL_TYPES is the type select's full offer: the domain's types, with the
// role-less ones marked so tagging a model is not a silent dead end.
export const MODEL_TYPES = [
  { value: '', text: 'Not declared (shown in picker)' },
  { value: 'text', text: 'Text / chat' },
  { value: 'image', text: 'Image' },
  { value: 'audio', text: 'Audio · not used by a role yet' },
  { value: 'decision', text: 'Decision (fixed choices) · not used by a role yet' },
  { value: 'realtime', text: 'Realtime (live session) · not used by a role yet' },
  { value: 'embedding', text: 'Embedding' },
  { value: 'rerank', text: 'Rerank · not used by a role yet' },
  { value: 'speech', text: 'Speech (TTS)' },
  { value: 'transcription', text: 'Transcription (STT)' },
  { value: 'video', text: 'Video' },
];
