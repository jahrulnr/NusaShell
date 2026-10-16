import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { contextInspectorModel, renderContextInspector } from '../page/conversation/context-inspector.js';

// The context inspector turns three honest data surfaces into one view model:
// provider-measured context fill vs the preflight estimate event, the three
// preflight categories (system/messages/tools), and the last usage (whole
// turn from turn.done vs the saved response usage from the snapshot).
// Unknown is never rendered as zero.

const models = [
  { id: 'm-priced', provider_id: 'prov-a', provider_name: 'Prov A', input_cost: 1, output_cost: 2, cache_read_cost: 0.1, context: 200000 },
  { id: 'm-free', provider_id: 'prov-a', input_cost: 0, output_cost: 0, cache_read_cost: 0, context: 128000 },
  { id: 'm-unpriced', provider_id: 'prov-b', context: 64000 },
  // Same model id served by a second provider with different pricing — a
  // bare-id lookup must never borrow this row for prov-a usage.
  { id: 'm-priced', provider_id: 'prov-c', provider_name: 'Prov C', input_cost: 100, output_cost: 200, cache_read_cost: 10, context: 50000 },
];

function render(host, model) {
  renderContextInspector(host, model);
  return host.querySelector('.context-inspector');
}

function valueFor(root, key) {
  for (const li of root.querySelectorAll('.context-list li')) {
    if (li.querySelector('.context-key')?.textContent === key) {
      return li.querySelector('.context-value');
    }
  }
  return null;
}

test('missing fill and missing window are unavailable, never zero', () => {
  const noTokens = contextInspectorModel({ windowTokens: 200000 });
  assert.equal(noTokens.fill.tokens, null);
  assert.equal(noTokens.fill.percent, null);
  assert.equal(noTokens.fill.source, 'unavailable');

  const noWindow = contextInspectorModel({ tokens: 1200, estimated: false, windowTokens: 0 });
  assert.equal(noWindow.fill.tokens, 1200);
  assert.equal(noWindow.fill.window, null);
  assert.equal(noWindow.fill.percent, null, 'missing window must not fabricate 0%');

  const dom = new JSDOM('<div id="panel"></div>');
  const host = dom.window.document.getElementById('panel');
  const root = render(host, noWindow);
  const line = root.querySelector('.context-usage-line span:last-child');
  assert.equal(line.textContent, 'unavailable');
  assert.equal(root.querySelector('.context-usage-track').hidden, true);
  assert.equal(valueFor(root, 'Used').textContent, '1.2k ctx');
});

test('numeric zero fill renders as 0%, not unavailable', () => {
  const model = contextInspectorModel({ tokens: 0, estimated: false, windowTokens: 200000 });
  assert.equal(model.fill.percent, 0);
  assert.equal(model.fill.source, 'measured');

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  assert.equal(root.querySelector('.context-usage-line span:last-child').textContent, '0%');
  assert.equal(root.querySelector('.context-usage-track').hidden, false);
  assert.equal(root.querySelector('.context-usage-track span').style.width, '0%');
});

test('fill source distinguishes measured response from estimated next request', () => {
  const measured = contextInspectorModel({ tokens: 1000, estimated: false, windowTokens: 10000 });
  assert.equal(measured.fill.source, 'measured');
  const estimated = contextInspectorModel({ tokens: 1000, estimated: true, windowTokens: 10000 });
  assert.equal(estimated.fill.source, 'estimated');
  assert.equal(estimated.fill.percent, 10);
});

test('estimate categories: an omitted category is zero only when supplied categories sum to the total', () => {
  const inferZero = contextInspectorModel({
    estimateEvent: { estimated_tokens: 1000, system_tokens: 600, messages_tokens: 400 },
  });
  assert.equal(inferZero.estimate.total, 1000);
  assert.deepEqual(
    inferZero.estimate.categories.map((c) => [c.id, c.tokens]),
    [['system', 600], ['conversation', 400], ['tools', 0]],
    'tools omitted by omitempty is provably zero: supplied sum equals the total',
  );

  const partial = contextInspectorModel({
    estimateEvent: { estimated_tokens: 1000, system_tokens: 600 },
  });
  assert.equal(partial.estimate.categories[0].tokens, 600);
  assert.equal(partial.estimate.categories[1].tokens, null, 'remainder cannot be attributed — unavailable');
  assert.equal(partial.estimate.categories[2].tokens, null);
});

test('estimate group is omitted until a live estimate event supplies a total', () => {
  // The backend emits agent.context.estimate only while a turn streams and
  // never persists it, so before the first live event the whole group must
  // be absent — a block of "unavailable" rows pretends the surface is
  // supported when nothing has supplied it.
  const none = contextInspectorModel({});
  assert.equal(none.estimate.total, null);
  assert.ok(none.estimate.categories.every((c) => c.tokens === null));

  const dom = new JSDOM('<div id="panel"></div>');
  const host = dom.window.document.getElementById('panel');
  const empty = render(host, none);
  const labels = [...empty.querySelectorAll('.context-group-label')].map((el) => el.textContent);
  assert.deepEqual(labels, ['Context fill', 'Latest usage · measured']);
  for (const key of ['Total', 'System', 'Conversation', 'Tool definitions']) {
    assert.equal(valueFor(empty, key), null, `${key} has no row without an estimate event`);
  }
});

test('estimate categories: unresolved shares are omitted, never rendered as unavailable', () => {
  // A bare total is real evidence, but a category the supplied shares cannot
  // attribute stays out of the list rather than showing an unavailable row.
  const bare = contextInspectorModel({ estimateEvent: { estimated_tokens: 900 } });
  assert.equal(bare.estimate.total, 900);
  assert.ok(bare.estimate.categories.every((c) => c.tokens === null));

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), bare);
  assert.equal(valueFor(root, 'Total').textContent, '~900');
  assert.equal(valueFor(root, 'System'), null);
  assert.equal(valueFor(root, 'Conversation'), null);
  assert.equal(valueFor(root, 'Tool definitions'), null);

  const partial = contextInspectorModel({ estimateEvent: { estimated_tokens: 1000, system_tokens: 600 } });
  const partialRoot = render(dom.window.document.getElementById('panel'), partial);
  assert.equal(valueFor(partialRoot, 'System').textContent, '~600');
  assert.equal(valueFor(partialRoot, 'Conversation'), null, 'unattributable remainder is omitted');
});

test('estimate categories: a zero total proves every omitted category is zero', () => {
  const model = contextInspectorModel({ estimateEvent: { estimated_tokens: 0 } });
  assert.deepEqual(model.estimate.categories.map((c) => c.tokens), [0, 0, 0]);
});

test('estimate categories render with the estimate marker', () => {
  const model = contextInspectorModel({
    estimateEvent: { estimated_tokens: 5200, system_tokens: 4200, messages_tokens: 700, tools_tokens: 300 },
  });
  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  assert.equal(valueFor(root, 'System').textContent, '~4.2k');
  assert.equal(valueFor(root, 'Tool definitions').textContent, '~300');
});

test('live turn.done usage is the whole turn; a present usage object zeroes omitted fields', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1200 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  assert.equal(model.usage.source, 'turn');
  assert.equal(model.usage.input, 1200);
  assert.equal(model.usage.output, 0, 'omitted token fields on a present UsageDTO are zero');
  assert.equal(model.usage.cacheRead, 0);
  assert.equal(model.usage.cacheWrite, 0);

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  assert.equal(valueFor(root, 'Last turn').textContent, '1.2k in · 0 out');
});

test('absent usage is unknown, not zero', () => {
  const absent = contextInspectorModel({ turnUsage: { usage: null, model: 'm-priced' }, models });
  assert.equal(absent.usage.source, 'none');
  const missing = contextInspectorModel({ models });
  assert.equal(missing.usage.source, 'none');

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), missing);
  assert.equal(valueFor(root, 'Last turn'), null, 'no fabricated empty usage row');
  assert.equal(valueFor(root, 'Est. cost').textContent, 'unavailable');
});

test('snapshot usage is the saved response, labeled distinctly from the whole turn', () => {
  const snapshotMessages = [
    { id: 'u1', role: 'user', content: 'hi' },
    { id: 'a1', role: 'assistant', content: 'old', usage: { input_tokens: 10, output_tokens: 5 }, model: 'm-priced', provider_id: 'prov-a' },
    { id: 'a2', role: 'assistant', content: 'no usage yet' },
    { id: 'a3', role: 'assistant', content: 'new', usage: { input_tokens: 50, output_tokens: 10, cache_read: 7 }, model: 'm-priced', provider_id: 'prov-a' },
  ];
  const model = contextInspectorModel({ snapshotMessages, models });
  assert.equal(model.usage.source, 'saved-response');
  assert.equal(model.usage.input, 50);
  assert.equal(model.usage.cacheRead, 7);
  assert.equal(model.usage.model, 'm-priced');

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  assert.equal(valueFor(root, 'Last saved response').textContent, '50 in · 10 out');
  assert.equal(valueFor(root, 'Last turn'), null, 'snapshot is not relabeled as a whole turn');
});

test('live whole-turn usage wins over a stale snapshot usage', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 900, output_tokens: 30 }, model: 'm-priced', providerId: 'prov-a' },
    snapshotMessages: [
      { id: 'a1', role: 'assistant', usage: { input_tokens: 50, output_tokens: 10 }, model: 'm-priced', provider_id: 'prov-a' },
    ],
    models,
  });
  assert.equal(model.usage.source, 'turn');
  assert.equal(model.usage.input, 900);
});

test('cost is priced from the message/run model identity, not the selected model', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000, output_tokens: 1_000_000 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  assert.equal(model.usage.cost.usd, 3, 'm-priced: 1M in @ $1 + 1M out @ $2');
});

test('zero price is free; absent price is unavailable', () => {
  const free = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 5000, output_tokens: 500 }, model: 'm-free', providerId: 'prov-a' },
    models,
  });
  assert.equal(free.usage.cost.usd, 0);

  const unpriced = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 5000, output_tokens: 500 }, model: 'm-unpriced', providerId: 'prov-b' },
    models,
  });
  assert.equal(unpriced.usage.cost.usd, null);

  const unknownModel = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 5 }, model: 'ghost', providerId: 'prov-z' },
    models,
  });
  assert.equal(unknownModel.usage.cost.usd, null);

  const dom = new JSDOM('<div id="panel"></div>');
  const freeRoot = render(dom.window.document.getElementById('panel'), free);
  assert.equal(valueFor(freeRoot, 'Est. cost').textContent, '$0');
  const unpricedRoot = render(dom.window.document.getElementById('panel'), unpriced);
  assert.equal(valueFor(unpricedRoot, 'Est. cost').textContent, 'unavailable');
});

test('cache read is priced at its own rate and never double-billed as input', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000, output_tokens: 0, cache_read: 1_000_000, cache_write: 400 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  // input_tokens already excludes cache_read (the wire normalizes it out), so
  // the cost is input @ $1 + cache_read @ $0.1 — never 2M at the input rate.
  assert.equal(model.usage.cost.usd, 1.1);
  assert.equal(model.usage.cost.unpricedCacheWrite, true, 'cache write has no catalog price field');
});

test('a missing price for a used leg makes the whole cost unavailable', () => {
  const outputOnly = contextInspectorModel({
    turnUsage: { usage: { output_tokens: 500 }, model: 'm-unpriced', providerId: 'prov-b' },
    models,
  });
  assert.equal(outputOnly.usage.cost.usd, null);

  // Zero-token legs do not demand a price: input-only usage still prices.
  const inputOnly = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  assert.equal(inputOnly.usage.cost.usd, 1);
});

test('render uses textContent for dynamic data and current dock classes', () => {
  const model = contextInspectorModel({
    tokens: 1200, estimated: true, windowTokens: 200000,
    estimateEvent: { estimated_tokens: 1300, system_tokens: 1300 },
    turnUsage: { usage: { input_tokens: 7, output_tokens: 3 }, model: 'm-<img onerror=x>', providerId: 'prov-a' },
    models,
  });
  const dom = new JSDOM('<div id="panel"></div>');
  const host = dom.window.document.getElementById('panel');
  const root = render(host, model);

  assert.ok(root.querySelector('.context-usage'));
  assert.ok(root.querySelector('.context-list'));
  const modelValue = valueFor(root, 'Model');
  assert.equal(modelValue.textContent, 'prov-a:m-<img onerror=x>');
  assert.equal(modelValue.querySelector('img'), null, 'dynamic data is textContent, never markup');
  assert.equal(host.querySelector('img'), null);
});

test('a known provider never prices against another provider\'s same-id model', () => {
  // turn.done carries no provider_id; when the run's provider is captured the
  // catalog match stays provider-exact even though prov-c serves the same id
  // at a 100x rate.
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  assert.equal(model.usage.cost.usd, 1, 'prov-a rate, not prov-c');

  const other = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced', providerId: 'prov-c' },
    models,
  });
  assert.equal(other.usage.cost.usd, 100, 'prov-c rate, not prov-a');

  // A provider that is absent from the catalog must not fall back to a
  // same-id row owned by another provider.
  const missing = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced', providerId: 'prov-gone' },
    models,
  });
  assert.equal(missing.usage.cost.usd, null, 'no cross-provider fallback');
});

test('an unknown provider prices only an unambiguous catalog id', () => {
  // m-priced exists under two providers, so a provider-less usage cannot be
  // attributed to either rate.
  const ambiguous = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced' },
    models,
  });
  assert.equal(ambiguous.usage.cost.usd, null, 'duplicate ids across providers stay unavailable');

  // m-unpriced exists once; the id is unambiguous but carries no rates.
  const uniqueUnpriced = contextInspectorModel({
    turnUsage: { usage: { output_tokens: 5 }, model: 'm-unpriced' },
    models,
  });
  assert.equal(uniqueUnpriced.usage.cost.usd, null);

  const uniqueModels = [...models, { id: 'm-solo', provider_id: 'prov-d', input_cost: 4, output_cost: 8 }];
  const unique = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 500_000 }, model: 'm-solo' },
    models: uniqueModels,
  });
  assert.equal(unique.usage.cost.usd, 2, 'a unique bare id still prices');
});

test('a qualified model identifier resolves its provider consistently', () => {
  // turn.done payloads normalize through the same provider:model identity
  // split as conversation models.
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'prov-a:m-priced' },
    models,
  });
  assert.equal(model.usage.providerId, 'prov-a');
  assert.equal(model.usage.model, 'm-priced');
  assert.equal(model.usage.cost.usd, 1);

  const snapshotMessages = [
    { id: 'a1', role: 'assistant', usage: { input_tokens: 1_000_000 }, model: 'prov-c:m-priced' },
  ];
  const saved = contextInspectorModel({ snapshotMessages, models });
  assert.equal(saved.usage.providerId, 'prov-c');
  assert.equal(saved.usage.model, 'm-priced');
  assert.equal(saved.usage.cost.usd, 100, 'snapshot uses the qualified provider, not a same-id fallback');
});

test('a snapshot message provider_id resolves the turn usage after refresh', () => {
  // turn.done has no provider_id on the wire; when the captured provider is
  // missing, the persisted message for the turn resolves it lazily once the
  // snapshot refresh lands.
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'm-priced', messageId: 'a9' },
    snapshotMessages: [
      { id: 'a9', role: 'assistant', usage: { input_tokens: 5 }, model: 'm-priced', provider_id: 'prov-c' },
    ],
    models,
  });
  assert.equal(model.usage.source, 'turn');
  assert.equal(model.usage.providerId, 'prov-c');
  assert.equal(model.usage.cost.usd, 100);
});

test('unpriced cache writes keep the partial cost visibly labelled', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000, cache_write: 800 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  assert.equal(model.usage.cost.usd, 1);
  assert.equal(model.usage.cost.unpricedCacheWrite, true);

  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  const cost = valueFor(root, 'Est. cost');
  assert.equal(cost.textContent, '$1.00 excl. cache writes',
    'an incomplete cost is visibly partial, not presented as the whole estimate');
  assert.match(cost.title, /cache write/i);
});

test('group headers separate the estimate from the measured usage', () => {
  const model = contextInspectorModel({
    tokens: 1200, estimated: true, windowTokens: 200000,
    estimateEvent: { estimated_tokens: 5200, system_tokens: 4200, messages_tokens: 700, tools_tokens: 300 },
    turnUsage: { usage: { input_tokens: 7, output_tokens: 3 }, model: 'm-priced', providerId: 'prov-a' },
    models,
  });
  const dom = new JSDOM('<div id="panel"></div>');
  const root = render(dom.window.document.getElementById('panel'), model);
  const headers = [...root.querySelectorAll('.context-group-label')].map((el) => el.textContent);
  assert.deepEqual(headers, ['Context fill', 'Next request · estimated', 'Latest usage · measured']);
  assert.equal(valueFor(root, 'Total').textContent, '~5.2k');
});

test('raw model ids with colons stay whole — explicit provider_id preserves :free', () => {
  const colonModels = [
    ...models,
    { id: 'vendor/model:free', provider_id: 'openrouter', input_cost: 0, output_cost: 0 },
    { id: 'vendor/model:thinking', provider_id: 'openrouter', input_cost: 1, output_cost: 2 },
  ];
  // The wire carries provider_id separately; `vendor/model:free` is the whole
  // model id, not a `vendor/model` provider serving a `free` model.
  const saved = contextInspectorModel({
    snapshotMessages: [
      { id: 'a1', role: 'assistant', usage: { input_tokens: 100 }, model: 'vendor/model:free', provider_id: 'openrouter' },
    ],
    models: colonModels,
  });
  assert.equal(saved.usage.model, 'vendor/model:free');
  assert.equal(saved.usage.providerId, 'openrouter');
  assert.equal(saved.usage.cost.usd, 0, 'priced against the explicit provider row, not a split-off :free id');

  const turn = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 500_000 }, model: 'vendor/model:thinking', providerId: 'openrouter' },
    models: colonModels,
  });
  assert.equal(turn.usage.model, 'vendor/model:thinking');
  assert.equal(turn.usage.providerId, 'openrouter');
  assert.equal(turn.usage.cost.usd, 0.5);
});

test('a qualified nested-colon model strips only the provider prefix', () => {
  const colonModels = [
    ...models,
    { id: 'vendor/model:free', provider_id: 'openrouter', input_cost: 0.5, output_cost: 1 },
  ];
  // openrouter:vendor/model:free → provider openrouter, model vendor/model:free.
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'openrouter:vendor/model:free' },
    models: colonModels,
  });
  assert.equal(model.usage.providerId, 'openrouter');
  assert.equal(model.usage.model, 'vendor/model:free');
  assert.equal(model.usage.cost.usd, 0.5);

  const saved = contextInspectorModel({
    snapshotMessages: [
      { id: 'a1', role: 'assistant', usage: { input_tokens: 1_000_000 }, model: 'openrouter:vendor/model:free' },
    ],
    models: colonModels,
  });
  assert.equal(saved.usage.model, 'vendor/model:free');
  assert.equal(saved.usage.providerId, 'openrouter');
  assert.equal(saved.usage.cost.usd, 0.5);
});

test('an unknown bare colon model stays raw and unpriced when ambiguous', () => {
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'vendor/model:free' },
    models,
  });
  assert.equal(model.usage.model, 'vendor/model:free', 'no provider is invented from the first colon');
  assert.equal(model.usage.providerId, null);
  assert.equal(model.usage.cost.usd, null, 'no catalog evidence means no price');
});

test('a provider prefix caught by a blind split cannot re-split the same id', () => {
  const colonModels = [
    ...models,
    { id: 'vendor/model:free', provider_id: 'openrouter', input_cost: 1, output_cost: 2 },
  ];
  // conversationProviderID('vendor/model:free') yields 'vendor/model' — a
  // prefix of the model id, not a catalog provider. It must not strip the
  // ':free' suffix and reprice the turn as a `free` model.
  const model = contextInspectorModel({
    turnUsage: { usage: { input_tokens: 1_000_000 }, model: 'vendor/model:free', providerId: 'vendor/model' },
    models: colonModels,
  });
  assert.equal(model.usage.model, 'vendor/model:free');
  assert.notEqual(model.usage.providerId, 'vendor/model');
  assert.equal(model.usage.cost.usd, 1, 'the unique bare id still prices under its real provider');
});

test('re-render replaces prior inspector content in place', () => {
  const dom = new JSDOM('<div id="panel"><div class="context-inspector"><p>stale</p></div></div>');
  const host = dom.window.document.getElementById('panel');
  render(host, contextInspectorModel({ tokens: 10, windowTokens: 100 }));
  assert.equal(host.querySelectorAll('.context-inspector').length, 1);
  assert.equal([...host.querySelectorAll('*')].some((el) => el.textContent === 'stale'), false);
  const again = contextInspectorModel({ tokens: 50, windowTokens: 100 });
  render(host, again);
  assert.equal(host.querySelectorAll('.context-inspector').length, 1);
  assert.equal(valueFor(host.querySelector('.context-inspector'), 'Used').textContent, '50/100 context');
});
