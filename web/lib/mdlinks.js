// Where a Markdown link or image in a thread points, and what the web UI
// shows for it. Both Markdown profiles (assistant prose in lib/markdown.js,
// owner text in lib/usermark.js) share it, so a path resolves the same way
// whoever wrote it. Nothing here touches the DOM, so tests drive it in Node.
//
// A local file is a path — absolute, ~/, a file: URL, a Windows drive path,
// or relative to the thread's workspace. Anything with another scheme is
// remote and is never fetched on the owner's behalf: an image of it shows
// as a link.

const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

// A destination another host serves. A colon alone is not a scheme: a
// relative file with an editor position ("notes.txt:3") and a Windows drive
// path both carry one and still name a local file.
const REMOTE = /^(?:https?|wss?|ftp|mailto|data|blob|javascript|tel|sms):/i;

// localTarget is the local file path a link destination names, or '' when
// it names something else: a remote address, a protocol-relative URL, the
// app's own /api/ and /#/ routes, or a bare #fragment or ?query. Markdown
// destinations are URLs, so a percent-encoded path is decoded back to the
// path as written.
export function localTarget(href) {
  const h = String(href ?? '').trim();
  if (!h || /^[#?]/.test(h) || h.startsWith('//') || /^\/(?:api\/|#)/i.test(h)) return '';
  if (REMOTE.test(h)) return '';
  try { return decodeURI(h); } catch { return h; }
}

const KINDS = {
  image: ['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'svg', 'bmp', 'ico', 'apng'],
  video: ['mp4', 'm4v', 'webm', 'mov', 'ogv', 'mkv'],
  audio: ['mp3', 'wav', 'ogg', 'oga', 'opus', 'm4a', 'aac', 'flac', 'weba'],
};
const KIND_OF = new Map(Object.entries(KINDS).flatMap(([k, exts]) => exts.map((e) => [e, k])));

// mediaKind is 'image', 'video', or 'audio' by the path's extension, else ''.
// An editor position ("clip.mp4:12") is not part of the name.
export function mediaKind(path) {
  const name = String(path ?? '').replace(/[?#].*$/, '').replace(/(?::\d+){1,2}$/, '');
  const ext = /\.([a-z0-9]+)$/i.exec(name)?.[1];
  return KIND_OF.get((ext || '').toLowerCase()) || '';
}

export const baseName = (path) => String(path ?? '').replace(/[\\/]+$/, '').split(/[\\/]/).pop() || String(path ?? '');

// renderImage is the image renderer both profiles use. A local picture,
// video, or sound becomes a placeholder that lib/markdown.js turns into
// the playable embed once the block is sanitized; another local file
// becomes a file link; a remote address becomes a plain link.
export function renderImage({ href, text }) {
  const path = localTarget(href);
  const kind = path ? mediaKind(path) : '';
  if (kind) {
    return `<span class="md-media" data-media="${esc(path)}" data-kind="${kind}" title="${esc(text)}">${esc(text || baseName(path))}</span>`;
  }
  if (path || /^(?:https?:)/i.test(String(href ?? '').trim())) {
    return `<a href="${esc(String(href).trim())}">${esc(text || (path ? baseName(path) : href))}</a>`;
  }
  return `<span class="md-image">[image: ${esc(text)}]</span>`;
}
