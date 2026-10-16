import { openZoomableMedia } from '../js/media-zoom.js';
import { renderMarkdown } from '../js/markdown.js';
import { adoptWidgetStyles } from '../shared/widget-styles.js?v=7';

function attachmentSource(attachment) {
  return attachment?.data_url || (attachment?.file_path ? `/local-file?path=${encodeURIComponent(attachment.file_path)}` : '');
}

function attachmentGallery(host, attachments) {
  const gallery = document.createElement('div');
  gallery.className = 'message-attachments';
  gallery.setAttribute('aria-label', `${attachments.length} attachment${attachments.length === 1 ? '' : 's'}`);
  for (const attachment of attachments) {
    const item = document.createElement('figure');
    item.className = `message-attachment attachment-${attachment.type || 'file'}`;
    item.dataset.attachment = attachment.name || attachment.file_path || 'attachment';
    const source = attachmentSource(attachment);
    if (attachment.type === 'image' && source) {
      const image = document.createElement('img'); image.src = source; image.alt = attachment.name || ''; image.loading = 'lazy';
      const zoom = document.createElement('button'); zoom.type = 'button'; zoom.className = 'attachment-zoom'; zoom.setAttribute('aria-label', `Zoom ${attachment.name || 'image'}`); zoom.textContent = '↗';
      zoom.addEventListener('click', () => openZoomableMedia({ src: image.src, alt: image.alt, caption: image.alt }));
      item.append(image, zoom);
    } else if (attachment.type === 'video' && source) {
      const video = document.createElement('video'); video.src = source; video.controls = true; video.preload = 'metadata'; item.append(video);
    } else if (attachment.type === 'audio' && source) {
      const audio = document.createElement('audio'); audio.src = source; audio.controls = true; audio.preload = 'metadata'; item.append(audio);
    } else {

      const open = document.createElement('button'); open.type = 'button'; open.className = 'attachment-file'; open.dataset.filePath = attachment.file_path || '';
      open.innerHTML = '<span class="attachment-kind"></span><span class="attachment-name"></span>';
      open.querySelector('.attachment-kind').textContent = attachment.type === 'folder' ? 'DIR' : 'FILE';
      open.querySelector('.attachment-name').textContent = attachment.name || attachment.file_path || 'Attachment';
      open.addEventListener('click', () => host.dispatchEvent(new CustomEvent(attachment.type === 'folder' ? 'artifact-open' : 'local-file-open', {
        bubbles: true, composed: true, detail: { path: attachment.file_path || '' },
      })));
      item.append(open);
    }
    if (attachment.type !== 'audio') {
      const caption = document.createElement('figcaption'); caption.textContent = attachment.name || ''; item.append(caption);
    }
    gallery.append(item);
  }
  return gallery;
}

class UserMessageWidget extends HTMLElement {
  set event(value) {
    this._event = value;
    if (this.isConnected) this.render();
  }

  connectedCallback() { this.render(); }

  render() {
    const event = this._event;
    if (!event) return;
    const root = this.shadowRoot || this.attachShadow({ mode: 'open' });
    if (!root.querySelector('.user-widget')) this.mount(root);
    root.querySelector('.widget-time').textContent = event.time;
    const body = root.querySelector('.message-body');
    const content = String(event.content || '');
    if (this._content !== content) {
      this._content = content;
      body.innerHTML = renderMarkdown(content);
    }
    body.hidden = !content.trim();
    const attachments = event.attachments || [];
    const attachmentKey = JSON.stringify(attachments.map(({ type, name, file_path, data_url }) => [type, name, file_path, data_url]));
    if (this._attachmentKey !== attachmentKey) {
      this._attachmentKey = attachmentKey;
      root.querySelector('.message-attachments')?.remove();
      if (attachments.length) body.after(attachmentGallery(this, attachments));
    }
  }

  mount(root) {
    adoptWidgetStyles(root, 'user');
    const article = document.createElement('article');
    article.className = 'widget user-widget';
    article.innerHTML = `
      <header class="widget-head"><span class="user-mark" aria-hidden="true">Y</span><span class="widget-label">You</span><span class="widget-time"></span></header>
      <div class="message-body message-markdown"></div>
      <footer><button type="button" class="edit-message">Edit in composer</button></footer>`;
    const body = article.querySelector('.message-body');
    body.addEventListener('click', (clickEvent) => {
      const link = clickEvent.target.closest?.('a[data-local-path]');
      if (!link) return;
      clickEvent.preventDefault();
      this.dispatchEvent(new CustomEvent('local-file-open', {
        bubbles: true,
        composed: true,
        detail: { path: link.dataset.localPath },
      }));
    });
    article.querySelector('.edit-message').addEventListener('click', () => {
      this.dispatchEvent(new CustomEvent('widget-compose', { bubbles: true, composed: true, detail: { content: this._event.content } }));
    });
    root.append(article);
  }
}

if (!customElements.get('user-message-widget')) customElements.define('user-message-widget', UserMessageWidget);
