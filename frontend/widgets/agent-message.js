import { updateMarkdownBody } from '../shared/markdown-body.js?v=7';
import { createLoadingController } from '../shared/loading-layer.js?v=1';
import { adoptWidgetStyles } from '../shared/widget-styles.js?v=7';

function messageMarkdown(event) {
  if (event.markdown) return event.markdown;
  return (event.blocks || []).map((block) => {
    if (block.type === 'list') return block.items.map((item) => `- ${item}`).join('\n');
    return block.text || '';
  }).filter(Boolean).join('\n\n');
}

class AgentMessageWidget extends HTMLElement {
  set event(value) {
    this._event = value;
    if (this.isConnected) this.render();
  }

  connectedCallback() { this.render(); }

  render() {
    const event = this._event;
    if (!event) return;
    const root = this.shadowRoot || this.attachShadow({ mode: 'open' });
    if (!root.querySelector('.agent-widget')) this.mount(root);
    const status = String(event.status || '').toLowerCase();
    const failed = status === 'error' || status === 'failed' || Boolean(String(event.error || '').trim());
    root.querySelector('.model').textContent = event.model || '';
    root.querySelector('.widget-time').textContent = event.time || '';
    const usage = root.querySelector('.usage');
    usage.textContent = event.usage || '';
    usage.title = event.usageTitle || '';
    if (event.usageTitle || event.usage) usage.setAttribute('aria-label', event.usageTitle || event.usage);
    else usage.removeAttribute('aria-label');
    const article = root.querySelector('.agent-widget');
    article.classList.toggle('is-error', failed);
    const answer = root.querySelector('.answer');
    this._loading ||= createLoadingController(answer, { label: 'Rendering response' });
    const render = () => updateMarkdownBody(answer, messageMarkdown(event));
    if (/^live(?:-|$)/.test(String(event.id || ''))) void render();
    else void this._loading.run(render);

    const errorState = root.querySelector('.agent-error-state');
    const errorMessage = root.querySelector('.agent-error-message');
    const retry = root.querySelector('.retry-answer');
    const copy = root.querySelector('.copy-answer');
    const markdown = messageMarkdown(event);
    errorState.hidden = !failed;
    errorMessage.textContent = failed ? (event.error || 'Turn failed') : '';
    retry.hidden = !failed || event.canRetry === false || !event.messageId;
    retry.disabled = Boolean(this._retryPending);
    retry.textContent = this._retryPending ? 'Retrying…' : 'Retry with model';
    copy.hidden = !markdown.trim();
    root.querySelector('footer').hidden = !markdown.trim() && !event.usage;
    if (!failed) this._retryPending = false;
  }

  mount(root) {
    adoptWidgetStyles(root, 'agent');
    const article = document.createElement('article');
    article.className = 'widget agent-widget';
    article.innerHTML = `
      <header class="widget-head"><span class="agent-mark" aria-hidden="true">N</span><span class="widget-label">NusaShell</span><span class="model"></span><span class="widget-time"></span></header>
      <div class="answer agent-bubble-text"></div>
      <section class="agent-error-state" role="alert" hidden>
        <span class="agent-error-mark" aria-hidden="true">!</span>
        <div class="agent-error-copy"><strong>Turn failed</strong><p class="agent-error-message"></p></div>
        <button type="button" class="retry-answer">Retry with model</button>
      </section>
      <footer><span class="usage"></span><button type="button" class="copy-answer">Copy</button></footer>`;
    const answer = article.querySelector('.answer');
    answer.addEventListener('click', (event) => {
      const link = event.target.closest?.('a[data-local-path]');
      if (!link) return;
      event.preventDefault();
      this.dispatchEvent(new CustomEvent('local-file-open', { bubbles: true, composed: true, detail: { path: link.dataset.localPath } }));
    });
    article.querySelector('.copy-answer').addEventListener('click', async (event) => {
      const button = event.currentTarget;
      await navigator.clipboard?.writeText(messageMarkdown(this._event));
      button.textContent = 'Copied';
    });
    article.querySelector('.retry-answer').addEventListener('click', () => {
      if (this._retryPending) return;
      this.dispatchEvent(new CustomEvent('retry-turn', {
        bubbles: true,
        composed: true,
        detail: { messageId: this._event?.messageId || '', widget: this },
      }));
    });
    root.append(article);
  }

  setRetryPending(pending) {
    this._retryPending = Boolean(pending);
    const button = this.shadowRoot?.querySelector('.retry-answer');
    if (!button) return;
    button.disabled = this._retryPending;
    button.textContent = this._retryPending ? 'Retrying…' : 'Retry with model';
  }
}

if (!customElements.get('agent-message-widget')) customElements.define('agent-message-widget', AgentMessageWidget);
