import { bindLocalFileLinks, renderMarkdownBody, updateMarkdownBody } from '../shared/markdown-body.js?v=7';
import { createLoadingController } from '../shared/loading-layer.js?v=1';
import { adoptWidgetStyles } from '../shared/widget-styles.js?v=7';
import { renderMarkdown } from '../js/markdown.js';

function markdownPreviewText(markdown) {
  const source = String(markdown ?? '').trim();
  if (!source) return '';
  const holder = document.createElement('span');
  holder.innerHTML = renderMarkdown(source);
  return (holder.textContent || '')
    .replace(/<\/?[a-z][^>]*>/gi, '')
    .replace(/\s+/g, ' ')
    .trim();
}

class ReasoningWidget extends HTMLElement {
  set event(value) {
    this._event = value;
    if (this.isConnected) this.render();
  }

  connectedCallback() {
    this.render();
  }

  render() {
    const event = this._event;
    if (!event) return;
    const root = this.shadowRoot || this.attachShadow({ mode: 'open' });
    let article = root.querySelector('article');
    if (!article) {
      adoptWidgetStyles(root, 'reasoning');
      article = document.createElement('article');
      article.className = 'widget reasoning-widget';
      const detailId = `reasoning-${String(event.id).replace(/[^a-zA-Z0-9_-]/g, '-')}`;
      article.innerHTML = `
      <button class="reasoning-toggle" type="button" aria-expanded="false" aria-controls="${detailId}">
        <span class="reasoning-icon" aria-hidden="true">⌁</span>
        <span class="reasoning-label">Reasoning</span>
        <span class="reasoning-summary"></span>
        <span class="reasoning-duration"></span>
        <span class="chevron" aria-hidden="true">⌄</span>
      </button>
      <div class="reasoning-details" id="${detailId}" hidden></div>`;
      const button = article.querySelector('button');
      const details = article.querySelector('.reasoning-details');
      // Markdown links inside reasoning (absolute data-local-path anchors and
      // workspace-relative file hrefs) open in the shared file preview instead
      // of navigating the shell; the handler delegates from the app.
      bindLocalFileLinks(details, this);
      button.addEventListener('click', () => {
        const expanded = button.getAttribute('aria-expanded') !== 'true';
        button.setAttribute('aria-expanded', String(expanded));
        details.hidden = !expanded;
        if (expanded) this.renderDetails(details);
        else {
          details.replaceChildren();
          delete details._reasoningMarkdown;
        }
      });
      root.append(article);
    }
    article.querySelector('.reasoning-summary').textContent = markdownPreviewText(event.summary);
    article.querySelector('.reasoning-duration').textContent = event.duration;
    this._details = event.details || [];
    const details = article.querySelector('.reasoning-details');
    if (!details.hidden) this.renderDetails(details);
  }

  renderDetails(container) {
    const markdown = (this._details || []).join('\n\n');
    if (container._reasoningMarkdown === undefined) {
      container._reasoningMarkdown = markdown;
      container._loading ||= createLoadingController(container, { label: 'Rendering reasoning' });
      const render = () => renderMarkdownBody(container, markdown);
      if (/^live(?:-|$)/.test(String(this._event?.id || ''))) void render();
      else void container._loading.run(render);
      return;
    }
    if (container._reasoningMarkdown !== markdown) {
      container._reasoningMarkdown = markdown;
      container._loading ||= createLoadingController(container, { label: 'Rendering reasoning' });
      const render = () => updateMarkdownBody(container, markdown);
      if (/^live(?:-|$)/.test(String(this._event?.id || ''))) void render();
      else void container._loading.run(render);
    }
  }
}

if (!customElements.get('reasoning-widget')) customElements.define('reasoning-widget', ReasoningWidget);
