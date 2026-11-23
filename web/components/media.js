// Media a thread shows: what the owner attached, what a reply sent to a chat,
// and what a tool put in front of the model (a read_media file, a browser
// screenshot). Every piece opens in the preview overlay when clicked; none of
// it is a link to a new tab. A file is served through the files API by its
// stored reference, so what shows is what the model was given.
import { html, useState } from '/vendor/preact/htm-preact.mjs';
import { fileURL } from '../lib/api.js';
import { Icon } from './icons.js';
import { openPreview } from './preview.js';
import { cx } from './ui.js';

// Where a piece of media is served from: the local preview of a file still
// being attached, else the files API for a stored file. A remote address is
// never fetched on the owner's behalf — it shows as a name only.
const sourceOf = (room, a) => a.preview || (a.uri?.startsWith('file:') && room.id ? fileURL(room.id, a.uri) : '');
const nameOf = (a) => a.name || (a.uri || '').split('/').pop() || 'file';
const kindOf = (a) => (a.modality || (a.mediaType || '').split('/')[0] || 'file');
const CHIP_ICON = { audio: 'volume', video: 'video', image: 'image' };

// Sizes: a message's own files, a tool's output in its card, and the strip
// under a collapsed run of tools.
const SIZE = {
  message: { img: 'h-28 max-w-56', video: 'h-28 max-w-56' },
  tool: { img: 'max-h-56 max-w-full', video: 'max-h-56 max-w-full' },
  strip: { img: 'h-12 max-w-24', video: 'h-12 max-w-24' },
};

function Chip({ icon, name, onClick, title }) {
  const inner = html`<${Icon} name=${icon} size=${13} class="shrink-0 text-muted" /><span class="truncate">${name}</span>`;
  const cls = 'inline-flex items-center gap-1.5 h-8 max-w-64 px-2.5 rounded-lg border border-line bg-panel text-[13px]';
  return onClick
    ? html`<button type="button" onClick=${onClick} title=${title || name} class=${cx(cls, 'hover:bg-hover')}>${inner}</button>`
    : html`<span class=${cls} title=${title || name}>${inner}</span>`;
}

// Media is one piece of media: a thumbnail for a picture or a video, a chip
// for anything else, and a click that opens the overlay.
export function Media({ room, a, size = 'message' }) {
  const [broken, setBroken] = useState(false);
  const src = sourceOf(room, a);
  const name = nameOf(a);
  const kind = kindOf(a);
  if (!src) return html`<${Chip} icon=${CHIP_ICON[kind] || 'paperclip'} name=${name} title=${a.uri} />`;
  const open = () => openPreview(src, name);
  const frame = 'relative block rounded-xl overflow-hidden border border-line bg-panel';
  if (kind === 'image' && !broken) {
    return html`<button type="button" onClick=${open} title=${name} class=${frame}>
      <img src=${src} alt=${name} loading="lazy" onError=${() => setBroken(true)} class=${cx(SIZE[size].img, 'object-cover')} />
    </button>`;
  }
  if (kind === 'video' && !broken) {
    // The first frame stands in for the clip; the overlay plays it.
    return html`<button type="button" onClick=${open} title=${name} class=${frame}>
      <video src=${src + '#t=0.1'} muted playsinline preload="metadata" onError=${() => setBroken(true)} class=${cx(SIZE[size].video, 'object-cover pointer-events-none')}></video>
      <span class="absolute inset-0 flex items-center justify-center bg-black/25 text-white"><${Icon} name="play" size=${size === 'strip' ? 14 : 22} /></span>
    </button>`;
  }
  return html`<${Chip} icon=${broken ? 'alert' : CHIP_ICON[kind] || 'paperclip'} name=${name} onClick=${open}
    title=${broken ? name + ' — the file is no longer there' : name} />`;
}

// Attachments of a message: stored ones by their file URI, pending ones by
// their local preview. User files sit right, files a reply sent left.
export function Attachments({ room, list, start = false }) {
  if (!list?.length) return null;
  return html`<div class=${cx('flex flex-wrap gap-1.5 max-w-[85%]', start ? 'justify-start' : 'justify-end')}>
    ${list.map((a, i) => html`<${Media} key=${i} room=${room} a=${a} />`)}
  </div>`;
}

// ToolMedia is what a tool showed the model. In a tool's card it is large;
// under a collapsed run of tools it is a strip of thumbnails, so the owner
// sees what the model saw without opening each call.
export function ToolMedia({ room, list, size = 'tool' }) {
  if (!list?.length) return null;
  return html`<div class="flex flex-wrap gap-2">
    ${list.map((a, i) => html`<${Media} key=${i} room=${room} a=${a} size=${size} />`)}
  </div>`;
}
