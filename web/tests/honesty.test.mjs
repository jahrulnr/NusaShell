// ACP honesty cards and the blind-room words, against the design's sentences.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { agentTimeSeconds, honestyCard, liveStatus, roomBusy, rowStatus, shortClass } from '../lib/chat.js';

const prompt = { id: 'm1', role: 'user', content: 'hello' };
const answer = { id: 'm2', role: 'assistant', content: 'done' };

test('the clock word is the server clock, and a missing stamp is not a counter', () => {
  const preparing = liveStatus({
    info: { runtime: 'agent' },
    live: { clock: 'preparing', startedAt: 1000 },
  }, 4000);
  assert.equal(preparing.text, 'Preparing the agent');
  assert.equal(preparing.counter, 3);

  const waiting = liveStatus({
    info: { runtime: 'agent' },
    live: { clock: 'waiting', promptedAt: 1000 },
  }, 6000);
  assert.equal(waiting.text, 'Waiting for the agent');
  assert.equal(waiting.note, 'no update for 5s');

  const wrong = liveStatus({
    info: { runtime: 'agent' },
    live: { clock: 'working' },
  }, 9000);
  assert.equal(wrong.text, 'In progress');
  assert.equal(wrong.counter, undefined);

  const gap = liveStatus({ info: { runtime: 'agent' }, live: {} }, 9000);
  assert.equal(gap.text, 'In progress');

  const provider = liveStatus({ info: { runtime: 'harness' }, live: { phase: { phase: 'reasoning' } } });
  assert.equal(provider.provider, true);

  assert.equal(agentTimeSeconds(1000, 4000), 3);
  assert.equal(agentTimeSeconds(1000, undefined), null);
  assert.equal(agentTimeSeconds(undefined, 4000), null);
});

test('auto retry is a countdown and never a Retry button', () => {
  const view = liveStatus({
    info: { runtime: 'agent' },
    live: { phase: { phase: 'retry_wait' }, retryAt: 12_000 },
  }, 9_500);
  assert.equal(view.text, 'Rate limited by the agent: retrying in 3s');
  const quiet = liveStatus({
    info: { runtime: 'agent', retry: 'auto' },
    live: { phase: { phase: 'retry_wait' } },
  }, 9_500);
  assert.equal(quiet.text, 'Rate limited by the agent: retrying');
});

test('cards use the class sentences, and quota and credential have no Retry', () => {
  const room = (over) => ({
    id: 'c1',
    messages: [prompt],
    info: { runtime: 'agent', lifecycle: 'failed', waiting: 2, ...over },
    failed: { agentClass: over.agentClass, retry: 'none', agentMessage: over.agentMessage, retryAt: over.retryAt, promptedAt: over.promptedAt, lastUpdateAt: over.lastUpdateAt, userMessagePersisted: over.userMessagePersisted !== false },
  });
  const now = 10_000;
  const quota = honestyCard(room({ agentClass: 'quota_exhausted', agentMessage: 'out' }), now);
  assert.equal(quota.title, 'Agent quota used up');
  assert.equal(quota.retry, false);
  assert.equal(quota.queue, 2);

  const signin = honestyCard(room({ agentClass: 'credential' }), now);
  assert.equal(signin.title, 'Agent sign-in refused');
  assert.equal(signin.body, "This is the agent's sign-in, not your NusaShell login.");
  assert.equal(signin.retry, false);

  const limited = honestyCard(room({ agentClass: 'rate_limited', retryAt: 15_000, agentMessage: 'slow down' }), now);
  assert.equal(limited.title, 'Rate limited by the agent');
  assert.equal(limited.body, 'The agent asked to wait 5s.');
  assert.equal(limited.message, 'slow down');
  assert.equal(limited.retry, true);
  assert.equal(roomBusy(room({ agentClass: 'rate_limited', retryAt: 15_000 })), false);

  const unnamed = honestyCard(room({ agentClass: 'rate_limited', agentMessage: '429 too many' }), now);
  assert.equal(unnamed.body, '429 too many');
  assert.equal(unnamed.message, '');
  assert.equal(unnamed.retry, true);
  assert.equal(JSON.stringify(unnamed).includes('did not say'), false);

  const blank = honestyCard(room({ agentClass: 'rate_limited' }), now);
  assert.equal(blank.body, '');
  assert.equal(blank.retry, true);
  assert.equal(JSON.stringify(blank).includes('did not say'), false);

  const elapsed = honestyCard(room({ agentClass: 'rate_limited', retryAt: 9_000, agentMessage: '429 too many' }), now);
  assert.equal(elapsed.body, '429 too many');
  assert.equal(elapsed.retry, true);

  const after = honestyCard(room({ agentClass: 'rate_limited', lastUpdateAt: 2, retryAt: 15_000, agentMessage: 'slow down' }), now);
  assert.equal(after.body, 'The agent asked to wait 5s.');
  assert.equal(after.retryOff, 'Automatic retry is off. Retry still sends.');
  assert.equal(after.retry, true);

  const started = honestyCard(room({ agentClass: 'process', promptedAt: 1 }), now);
  assert.equal(started.title, 'The agent never answered.');
  assert.equal(honestyCard(room({ agentClass: 'process' }), now).title, 'The agent did not start. Nothing was sent to it.');

  const seen = honestyCard(room({ agentClass: 'unknown', lastUpdateAt: 2, agentMessage: 'boom' }), now);
  assert.equal(seen.title, 'The agent reported an error');
  assert.equal(seen.message, 'boom');
  assert.equal(seen.retry, false);
  assert.equal(seen.retryOff, 'Retry is off, so nothing runs twice.');
  const startedSeen = honestyCard(room({ agentClass: 'process', lastUpdateAt: 2, promptedAt: 1 }), now);
  assert.equal(startedSeen.retry, false);
  assert.equal(startedSeen.retryOff, 'Retry is off, so nothing runs twice.');

  const silent = honestyCard(room({ agentClass: 'unknown', agentMessage: '  ' }), now);
  assert.equal(silent.body, 'The agent gave no words.');
  assert.equal(silent.retry, true);

  assert.equal(honestyCard({ ...room({ agentClass: 'quota_exhausted' }), messages: [prompt, answer] }), null);
  assert.equal(honestyCard({ id: 'c1', messages: [prompt], info: { lifecycle: 'idle', agentClass: 'rate_limited', retry: 'none' }, failed: null }), null);
  assert.equal(honestyCard(room({ agentClass: 'quota_exhausted', userMessagePersisted: false })), null);
});

test('the list row uses the lifecycle and the short class, and a blind agent room says Running', () => {
  assert.equal(rowStatus({ turnActive: true, runtime: 'agent' }), 'Running — no live view');
  assert.equal(rowStatus({ turnActive: true, runtime: 'harness' }), 'Working — no live view');
  assert.equal(rowStatus({ turnActive: true, runtime: '' }), '');
  assert.equal(rowStatus({ lifecycle: 'unknown', messageCount: 0 }), '');
  assert.equal(rowStatus({ lifecycle: 'unknown', messageCount: 3 }), 'Status unknown');
  assert.equal(rowStatus({ lifecycle: 'interrupted' }), 'interrupted');
  assert.equal(rowStatus({ lifecycle: 'idle', agentClass: 'process' }), 'idle · agent stopped');
  assert.equal(shortClass({ agentClass: 'rate_limited', retryAt: 20_000 }, 10_000), 'rate limited, retry in 10s');
  assert.equal(shortClass({ agentClass: 'unknown', agentMessage: 'first line\nsecond' }), 'first line');
  assert.equal(rowStatus({ lifecycle: 'failed' }).includes('idle'), false);
});
