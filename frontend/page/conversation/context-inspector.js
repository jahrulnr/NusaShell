// Context inspector — honest data preparation and rendering for the
// conversation Context dock. Three surfaces feed it, each labeled by source:
//
// - Fill: the provider-measured context fill of the last completed turn
//   (measured · prior response) or the provisional server preflight estimate
//   of the next request (estimated · next request). Missing data is
//   unavailable — never rendered as zero.
// - Categories: System / Conversation / Tool definitions from the live
//   agent.context.estimate event. Those fields are omitempty, so an absent
//   category is only provably zero when the supplied positive categories sum
//   exactly to estimated_tokens; otherwise it is omitted. The event is not
//   persisted — before the first live event of a session the whole group is
//   absent rather than rendered as unavailable rows.
// - Usage: the live turn.done UsageDTO (summed whole turn) or, after reload,
//   the latest assistant message usage in the snapshot (the saved response —
//   no run_id, cannot be relabeled as a whole turn). A present usage object
//   means its omitted token fields are zero; absent usage is unknown.
//   Estimated cost uses the catalog pricing of the message/run model, never
//   the currently selected model. input_tokens already excludes cache_read
//   (the wire subtracts it), so each leg is priced at its own rate without
//   double-billing; cache_write has no catalog price field. A numeric zero
//   price means free — absent means unavailable.

const CATEGORY_FIELDS = [
  { id: 'system', field: 'system_tokens', label: 'System' },
  { id: 'conversation', field: 'messages_tokens', label: 'Conversation' },
  { id: 'tools', field: 'tools_tokens', label: 'Tool definitions' },
];

function tokenNumber(value) {
  if (value === null || value === undefined || value === '') return null;
  const n = Number(value);
  return Number.isFinite(n) && n >= 0 ? n : null;
}

// estimateCategories resolves each preflight category or marks it unknown.
// Only the sum-equals-total invariant can prove an omitted category is zero.
function estimateCategories(event) {
  const total = event ? tokenNumber(event.estimated_tokens) : null;
  const categories = CATEGORY_FIELDS.map(({ id, field, label }) => ({
    id,
    label,
    tokens: event ? tokenNumber(event[field]) : null,
  }));
  if (event && total !== null) {
    const supplied = categories.reduce((sum, row) => sum + (row.tokens ?? 0), 0);
    if (supplied === total) {
      for (const row of categories) {
        if (row.tokens === null) row.tokens = 0;
      }
    }
  }
  return { total, categories };
}

// normalizeUsage maps a present UsageDTO to concrete numbers: omitempty drops
// zero-valued fields on the wire, so a missing key inside a present object is
// zero — but an absent object (handled by the caller) is unknown.
function normalizeUsage(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null;
  const field = (name) => Math.max(0, Math.floor(Number(raw[name]) || 0));
  return {
    input: field('input_tokens'),
    output: field('output_tokens'),
    cacheRead: field('cache_read'),
    cacheWrite: field('cache_write'),
  };
}

// splitQualifiedModel normalizes the `provider_id:model_id` identity used by
// conversation and message models. Provider model ids can carry their own
// colons (OpenRouter `vendor/model:free`, `vendor/model:thinking`), so a
// colon only separates a provider when the split is proven: a bare catalog
// id wins whole first, then a `provider:rest` prefix only when the prefix is
// the explicit/captured provider or the full string is a catalog identity.
// Anything else stays raw so an ambiguous id never invents a provider.
function splitQualifiedModel(model, { providerId = '', models = [] } = {}) {
  const raw = String(model || '').trim();
  const explicit = String(providerId || '').trim();
  const catalog = Array.isArray(models) ? models : [];
  if (!raw) return { providerId: explicit, modelId: '', qualified: false };
  if (catalog.some((entry) => entry?.id === raw)) {
    return { providerId: explicit, modelId: raw, qualified: false };
  }
  const sep = raw.indexOf(':');
  if (sep > 0 && sep < raw.length - 1) {
    const prefix = raw.slice(0, sep);
    const rest = raw.slice(sep + 1);
    if (prefix === explicit
      || catalog.some((entry) => entry?.provider_id === prefix && entry?.id === rest)) {
      return { providerId: prefix, modelId: rest, qualified: true };
    }
  }
  return { providerId: explicit, modelId: raw, qualified: false };
}

// latestSnapshotUsage returns the newest persisted assistant usage — the
// saved response only; nothing on the wire ties it to a whole turn.
function latestSnapshotUsage(messages, models = []) {
  const list = Array.isArray(messages) ? messages : [];
  for (let i = list.length - 1; i >= 0; i--) {
    const message = list[i];
    if (message?.role !== 'assistant') continue;
    const usage = normalizeUsage(message?.usage);
    if (usage) {
      const identity = splitQualifiedModel(message.model, { providerId: message.provider_id, models });
      return {
        usage,
        model: identity.modelId || '',
        providerId: String(message.provider_id || identity.providerId || ''),
      };
    }
  }
  return null;
}

// findCatalogModel prices a run against the catalog row for the provider that
// actually served it. A known provider requires an exact provider+id match —
// the same model id on another provider can carry a different rate. When the
// provider is unknown the id alone must resolve unambiguously; a duplicated
// bare id stays unpriced rather than borrowing a sibling provider's rates.
function findCatalogModel(models, providerId, modelId) {
  const id = String(modelId || '').trim();
  if (!id) return null;
  const provider = String(providerId || '').trim();
  if (provider) {
    return models.find((m) => m?.provider_id === provider && m?.id === id) || null;
  }
  const matches = models.filter((m) => m?.id === id);
  return matches.length === 1 ? matches[0] : null;
}

// estimateCost prices usage with the run/message model's catalog rates. Only
// legs that actually consumed tokens demand a price; a present numeric zero
// prices the leg as free. cache_write is tallied but never priced — the
// catalog exposes no cache-write rate — so its presence is flagged instead.
function estimateCost(entry, usage) {
  const result = { usd: null, unpricedCacheWrite: usage.cacheWrite > 0 };
  if (!entry) return result;
  const legs = [
    [usage.input, entry.input_cost],
    [usage.output, entry.output_cost],
    [usage.cacheRead, entry.cache_read_cost],
  ];
  let usd = 0;
  for (const [tokens, price] of legs) {
    if (!tokens) continue;
    if (typeof price !== 'number' || !Number.isFinite(price) || price < 0) return result;
    usd += (tokens / 1e6) * price;
  }
  result.usd = usd;
  return result;
}

// contextInspectorModel builds the view model. Callers pass the resolved fill
// (backendContextTokens tokens + estimated flag), the catalog window, the raw
// context.estimate event, the latest turn.done usage triple, the raw
// snapshotMessage list, and the model catalog.
export function contextInspectorModel({
  tokens = null,
  estimated = false,
  windowTokens = null,
  estimateEvent = null,
  turnUsage = null,
  snapshotMessages = [],
  models = [],
} = {}) {
  const fillTokens = tokenNumber(tokens);
  const windowSize = tokenNumber(windowTokens);
  const fill = {
    tokens: fillTokens,
    window: windowSize && windowSize > 0 ? windowSize : null,
    percent: fillTokens !== null && windowSize > 0 ? (fillTokens / windowSize) * 100 : null,
    source: fillTokens === null ? 'unavailable' : (estimated ? 'estimated' : 'measured'),
  };

  const estimate = estimateCategories(estimateEvent && typeof estimateEvent === 'object' ? estimateEvent : null);

  const catalog = Array.isArray(models) ? models : [];
  const liveUsage = turnUsage && typeof turnUsage === 'object' ? normalizeUsage(turnUsage.usage) : null;
  let usage;
  if (liveUsage) {
    // turn.done has no provider_id on the wire. Resolve it from the model
    // identity itself when qualified, then the persisted message for the
    // turn (once the post-turn snapshot lands), then the provider captured
    // when the run started — never the currently selected model. The
    // captured provider comes from a first-colon split of the conversation
    // model, so it only arbitrates a colon when the catalog actually lists
    // it as a provider; otherwise a raw `vendor/model:free` id would split
    // into a bogus `vendor/model` provider and a `free` model.
    const capturedRaw = String(turnUsage.providerId || turnUsage.provider_id || '');
    const captured = catalog.some((entry) => entry?.provider_id === capturedRaw) ? capturedRaw : '';
    const identity = splitQualifiedModel(turnUsage.model, { providerId: captured, models: catalog });
    const messageProvider = turnUsage.messageId
      ? String((Array.isArray(snapshotMessages) ? snapshotMessages : [])
        .find((message) => message?.id === turnUsage.messageId)?.provider_id || '')
      : '';
    usage = {
      source: 'turn',
      ...liveUsage,
      model: identity.modelId || null,
      providerId: identity.qualified
        ? identity.providerId
        : messageProvider || captured || null,
    };
  } else {
    const saved = latestSnapshotUsage(snapshotMessages, catalog);
    usage = saved
      ? { source: 'saved-response', ...saved.usage, model: saved.model || null, providerId: saved.providerId || null }
      : { source: 'none', input: null, output: null, cacheRead: null, cacheWrite: null, model: null, providerId: null };
  }
  usage.cost = usage.source === 'none'
    ? { usd: null, unpricedCacheWrite: false }
    : estimateCost(findCatalogModel(catalog, usage.providerId, usage.model), usage);

  return { fill, estimate, usage };
}

function formatTokens(value) {
  if (!Number.isFinite(value) || value <= 0) return '0';
  if (value >= 1_000_000) return `${Number((value / 1_000_000).toFixed(1))}M`;
  if (value >= 1_000) return `${Number((value / 1_000).toFixed(1))}k`;
  return String(value);
}

function formatUSD(usd) {
  if (usd === 0) return '$0';
  if (usd < 0.0001) return '<$0.0001';
  if (usd < 0.01) return `$${usd.toPrecision(2)}`;
  return `$${usd.toFixed(2)}`;
}

function percentLabel(percent) {
  if (percent === null) return 'unavailable';
  if (percent > 0 && percent < 1) return '<1%';
  return `${Math.round(percent)}%`;
}

// renderContextInspector fills `.context-inspector` inside host (creating it
// on first render, reusing it after) with the fill meter and the detail list.
// It only uses the existing dock classes and textContent for dynamic data.
export function renderContextInspector(host, model = contextInspectorModel()) {
  if (!host) return null;
  const doc = host.ownerDocument;
  let root = host.querySelector('.context-inspector');
  if (!root) {
    root = doc.createElement('div');
    root.className = 'context-inspector';
    host.append(root);
  }
  root.replaceChildren();

  const usageBox = doc.createElement('div');
  usageBox.className = 'context-usage';
  const line = doc.createElement('div');
  line.className = 'context-usage-line';
  const lineKey = doc.createElement('span');
  lineKey.textContent = 'Context';
  const lineValue = doc.createElement('span');
  lineValue.textContent = percentLabel(model.fill.percent);
  if (model.fill.percent === null) lineValue.dataset.availability = 'unavailable';
  line.append(lineKey, lineValue);
  const track = doc.createElement('div');
  track.className = 'context-usage-track';
  track.setAttribute('aria-hidden', 'true');
  track.hidden = model.fill.percent === null;
  const trackFill = doc.createElement('span');
  trackFill.style.width = `${Math.min(100, Math.max(0, model.fill.percent ?? 0))}%`;
  track.append(trackFill);
  usageBox.append(line, track);
  root.append(usageBox);

  let list = null;
  const group = (label) => {
    const head = doc.createElement('p');
    head.className = 'context-group-label';
    head.textContent = label;
    list = doc.createElement('ul');
    list.className = 'context-list';
    root.append(head, list);
  };
  const row = (key, text, { title = '', unavailable = false } = {}) => {
    const li = doc.createElement('li');
    const keyEl = doc.createElement('span');
    keyEl.className = 'context-key';
    keyEl.textContent = key;
    const valueEl = doc.createElement('span');
    valueEl.className = 'context-value';
    valueEl.textContent = text;
    if (title) valueEl.title = title;
    if (unavailable) valueEl.dataset.availability = 'unavailable';
    li.append(keyEl, valueEl);
    list.append(li);
    return { li, valueEl };
  };

  const { fill, estimate, usage } = model;
  group('Context fill');
  if (fill.tokens === null) {
    row('Used', 'unavailable', { unavailable: true });
  } else {
    const prefix = fill.source === 'estimated' && fill.tokens > 0 ? '~' : '';
    const used = fill.window
      ? `${prefix}${formatTokens(fill.tokens)}/${formatTokens(fill.window)} context`
      : `${prefix}${formatTokens(fill.tokens)} ctx`;
    const exact = fill.window ? `${fill.tokens} / ${fill.window}` : `${fill.tokens}`;
    row('Used', used, { title: `${exact} tokens` });
  }
  const sourceText = {
    measured: 'measured · prior response',
    estimated: 'estimated · next request',
    unavailable: 'unavailable',
  }[fill.source];
  row('Source', sourceText, { unavailable: fill.source === 'unavailable' });

  // The estimate event is live-only: it is emitted while a turn streams and
  // never persisted, so before the first event of a session the whole group
  // is omitted — a block of "unavailable" rows would pretend the surface is
  // supported when nothing has supplied it. A category the supplied shares
  // cannot attribute is likewise omitted rather than faked or dimmed.
  if (estimate.total !== null) {
    group('Next request · estimated');
    row('Total', `~${formatTokens(estimate.total)}`, {
      title: `${estimate.total} tokens — preflight estimate, next request`,
    });
    for (const category of estimate.categories) {
      if (category.tokens === null) continue;
      const { li } = row(category.label, `~${formatTokens(category.tokens)}`, {
        title: `${category.tokens} tokens — preflight estimate, next request`,
      });
      if (estimate.total > 0) {
        const meter = doc.createElement('span');
        meter.className = 'context-cat-meter';
        meter.dataset.cat = category.id;
        meter.setAttribute('aria-hidden', 'true');
        const meterFill = doc.createElement('span');
        meterFill.style.width = `${Math.min(100, (category.tokens / estimate.total) * 100)}%`;
        meter.append(meterFill);
        li.append(meter);
      }
    }
  }

  group('Latest usage · measured');
  if (usage.source !== 'none') {
    row(usage.source === 'turn' ? 'Last turn' : 'Last saved response',
      `${formatTokens(usage.input)} in · ${formatTokens(usage.output)} out`,
      { title: `${usage.input} input · ${usage.output} output tokens` });
    row('Cache', `${formatTokens(usage.cacheRead)} read · ${formatTokens(usage.cacheWrite)} write`,
      { title: `${usage.cacheRead} cache read · ${usage.cacheWrite} cache write tokens` });
    if (usage.model) {
      row('Model', usage.providerId ? `${usage.providerId}:${usage.model}` : usage.model);
    }
  }

  if (usage.cost.usd === null) {
    row('Est. cost', 'unavailable', {
      unavailable: true,
      title: usage.source === 'none' ? 'No usage yet' : 'No catalog pricing for this model',
    });
  } else {
    // cache_write has no catalog rate — a partial sum must read as partial in
    // the value itself, not only in the tooltip.
    row('Est. cost', usage.cost.unpricedCacheWrite ? `${formatUSD(usage.cost.usd)} excl. cache writes` : formatUSD(usage.cost.usd), {
      title: `Estimated from catalog pricing${usage.cost.unpricedCacheWrite ? ' — cache write tokens have no catalog rate and are excluded' : ''}`,
    });
  }

  return root;
}
