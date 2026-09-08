import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  LONG_COMPACTION_SUMMARY,
  startE2EHarness,
  startTurn,
  waitFor,
} from './e2e-harness.mjs';

test('embedded frontend completes one representative flow through the Go backend', async (t) => {
  const { rpcModule, server } = await startE2EHarness(t, { prefix: 'nusashell-e2e' });

  try {
    await waitFor(() => /skills$/.test(document.getElementById('skills-count')?.textContent || ''), 'initial skills view');

    window.location.hash = '#settings';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('settings-max-tool-rounds').value = '2';
    document.getElementById('settings-save-btn').click();
    await waitFor(() => document.getElementById('settings-save-status')?.textContent === 'Saved on this device.', 'settings save through the UI');
    assert.equal((await rpcModule.rpc('settings.get')).settings.max_tool_rounds, 2);

    // Skills CRUD is now install/delete only (no inline create form), so the
    // representative flow skips skill creation. Verify the logs view renders
    // live events from the conversation creation below instead.

    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(() => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((node) => node.textContent === 'Untitled'), 'new conversation through the UI');

    window.location.hash = '#logs';
    window.dispatchEvent(new window.Event('hashchange'));
    await waitFor(() => [...document.querySelectorAll('#log-tail .log-line .log-msg')].some((el) => el.textContent.includes('conversation created')), 'live log event in the UI');

    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    assert.equal(document.querySelectorAll('.view.active').length, 1);
    assert.match(document.getElementById('log-count').textContent, /entries/);

    // Let the WS close naturally so the offline screen can assert; harness
    // teardown disables reconnect and closes the socket afterward.
    server.go.kill();
    await waitFor(
      () => !document.getElementById('offline-screen')?.hidden,
      'full-window offline state after the local backend stops',
      20000,
    );
    assert.match(document.getElementById('offline-screen').textContent, /Sorry, it looks like your agent is offline\./);
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

test('compaction triggers and renders a marker when conversation exceeds threshold', async (t) => {
  const { llm, rpcModule, server } = await startE2EHarness(t, {
    prefix: 'nusashell-compaction-e2e',
  });

  try {
    // Seed history with compaction disabled, then enable with small context.
    await rpcModule.rpc('settings.set', { compaction_enabled: false });

    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Seed 4 turns with large messages (~4000 tokens total).
    const bigMsg = 'x'.repeat(2000);
    for (let i = 0; i < 4; i++) {
      llm.setScripts([[{ text: bigMsg }]]);
      await startTurn(rpcModule.rpc, {
        conversation_id: convID, text: bigMsg, model: 'tiny-model',
      });
      await waitFor(async () => {
        const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
        return c.conversation?.status === 'idle';
      }, `turn ${i + 1} done`, 15000);
    }

    // Enable compaction with a small context window (trigger = 800 tokens).
    // The seeded history (~4000 tokens) is well above the trigger.
    await rpcModule.rpc('settings.set', { max_input_tokens: 1000, compaction_enabled: true });

    // Set the compaction summary response.
    llm.setComplete(LONG_COMPACTION_SUMMARY);
    llm.setScripts([[{ text: 'Turn after compaction.' }]]);

    // Trigger compaction with a new user message.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'continue', model: 'tiny-model',
    });

    // Wait for the compaction marker to appear in the UI.
    await waitFor(
      () => [...document.querySelectorAll('#agent-thread .agent-compaction-marker')].some(
        (n) => n.textContent.includes('Compacted'),
      ),
      'compaction marker in the UI',
      15000,
    );
    // Verify it rendered as an assistant bubble, not a standalone pill.
    assert.ok(
      document.querySelector('#agent-thread .agent-message.agent-compaction-marker'),
      'compaction marker should be in an agent-message assistant bubble',
    );

    // Wait for the turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'post-compaction turn done', 15000);

    // Verify the conversation has the compaction marker in persisted state.
    // Compaction summaries carry role=user with a "[COMPACTION CHECKPOINT]"
    // prefix so they appear in the provider request's messages array.
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const compactionMsgs = gotten.messages?.filter(
      (m) => m.role === 'user' && m.content?.startsWith('[COMPACTION CHECKPOINT]'),
    ) || [];
    assert.ok(compactionMsgs.length > 0, 'at least one compaction summary (user) message exists');
    assert.ok(
      compactionMsgs.some((m) => m.content.includes('SUMMARY: user explored compaction e2e test')),
      `compaction marker not found: ${JSON.stringify(compactionMsgs.map((m) => m.content.slice(0, 80)))}`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// BH-AI-01: A stream cut mid-stream (no finish_reason, no [DONE]) that had
// accumulated tool-call deltas must surface as an error after the retry
// loop exhausts — never silently fall back to non-streaming, and never
// silently discard the accumulated tool calls.
//
// Architecture note: the old TS fallback layering (isIncompleteEmptyStream
// -> non-streaming retry) was removed; errors surface explicitly to the
// retry loop (see AGENTS.md). Compat streams intentionally treat
// finish_reason without [DONE] as a normal termination (many OpenAI-
// compatible gateways omit the sentinel), so this test simulates a TRUE
// mid-stream cut by omitting both the finish_reason chunk and [DONE].
test('BH-AI-01: incomplete stream with tool-call deltas must not silently fall back to non-streaming', async (t) => {
  // Large context: this test asserts streaming behavior, so the fresh-room
  // hydration checkpoint must NOT trip pre-turn compaction (a 1k window
  // would compact before the first stream and mask what is being tested).
  const { llm, rpcModule, server } = await startE2EHarness(t, {
    prefix: 'nusashell-bh-ai-01',
    llmOptions: { contextLength: 200000 },
  });

  try {
    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Script: stream a tool-call delta for the built-in "skill" family tool,
    // then cut the connection mid-stream: no finish_reason chunk and no
    // [DONE]. (finish_reason alone would be a normal termination for
    // compat streams — see the test header.)
    llm.setSendDone(false);
    llm.setSendFinish(false);
    llm.setScripts([[
      { toolCall: { id: 'call_bh01', name: 'skill', arguments: '{"op":"list"}' } },
    ]]);
    // Non-streaming fallback returns this text — if a fallback layer were
    // (re)introduced, the turn would silently produce this instead of
    // surfacing the error.
    llm.setComplete('FALLBACK_TEXT_FROM_NON_STREAMING');

    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'list skills', model: 'tiny-model',
    });

    // Wait for the turn to settle (either done or error).
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'turn settle', 15000);

    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const assistantMsgs = gotten.messages?.filter((m) => m.role === 'assistant') || [];
    const lastAssistant = assistantMsgs[assistantMsgs.length - 1];

    // Every streaming attempt is cut mid-stream (no finish_reason, no
    // [DONE]), so the compat stream surfaces a provider error on each
    // attempt. The retry loop exhausts (the fake LLM always cuts), the
    // partial-stream continuation fires once and fails too, and the turn
    // ends with status=error. The fallback text must NOT appear — there is
    // no non-streaming fallback layer, and the tool calls must not be
    // silently discarded.
    assert.ok(lastAssistant, 'an assistant message must exist');
    assert.notEqual(
      lastAssistant.content, 'FALLBACK_TEXT_FROM_NON_STREAMING',
      'BH-AI-01: must not silently fall back to non-streaming when tool calls were accumulated',
    );
    // The main chat must never receive a non-streaming request: the only
    // non-streaming consumer is compaction, and this test has none.
    const nonStreamReqs = llm.requests().filter((r) => !r.stream);
    assert.equal(
      nonStreamReqs.length, 0,
      'BH-AI-01: no non-streaming fallback request may be made for the main chat',
    );
    assert.equal(
      lastAssistant.status, 'error',
      `BH-AI-01: incomplete stream with tool calls must surface as an error, not silent success. ` +
        `status=${lastAssistant.status}, content=${JSON.stringify(lastAssistant.content)}`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// BH-SETTINGS-01: SettingsSetRequest uses *float64 with omitempty for
// sampling parameters (temperature, top_p, etc.). JSON null and JSON-absent
// both decode to nil, which handleSettingsSet treats as "don't change."
// There is no way to distinguish "clear to nil" from "leave unchanged" —
// once a sampling parameter is set, it cannot be cleared.
test('BH-SETTINGS-01: sampling parameters cannot be cleared to null once set', async (t) => {
  const { rpcModule, server } = await startE2EHarness(t, {
    prefix: 'nusashell-bh-settings-01',
  });

  try {
    // Step 1: Set temperature to 0.7.
    await rpcModule.rpc('settings.set', { temperature: 0.7 });
    const afterSet = await rpcModule.rpc('settings.get');
    assert.equal(afterSet.settings.temperature, 0.7, 'temperature must be 0.7 after setting');

    // Step 2: Try to clear temperature by sending null.
    // With the bug, null is treated as "don't change" because *float64 + omitempty
    // makes null indistinguishable from absent, and handleSettingsSet skips nil values.
    await rpcModule.rpc('settings.set', { temperature: null });

    // Step 3: After the fix, temperature is cleared (null/undefined in the
    // JSON response — *float64 with omitempty drops the field when nil).
    const afterClear = await rpcModule.rpc('settings.get');
    assert.equal(
      afterClear.settings.temperature, undefined,
      `BH-SETTINGS-01: temperature must be cleared after sending null (got ${afterClear.settings.temperature}). ` +
        `The settings.set contract must distinguish null (clear) from absent (don't change).`,
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// findHydration inspects a parsed chat/completions request body for the
// synthetic runtime-hydration transcript. Returns the hydration messages
// (assistant toolCalls + matching tool results) when present, or null when
// the request has no hydration exchange.
//
// Shape in the OpenAI request:
//   - assistant message with tool_calls whose ids start with "hydrate-"
//   - followed by tool messages with tool_call_id matching those ids.
// The transcript is DYNAMIC: slots whose real tool reports nothing (no
// plugins, no todos, empty memory documents) are omitted entirely. In this
// harness (fresh data dir, seeded user + soul documents, embedded skills, no
// plugins) the visible slots are runtime_context, file_list, file_read,
// file_read, skill.
function findHydration(messages) {
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (m.role !== 'assistant' || !Array.isArray(m.tool_calls) || m.tool_calls.length === 0) continue;
    const hydrateCalls = m.tool_calls.filter((c) => c.id?.startsWith('hydrate-'));
    if (hydrateCalls.length === 0) continue;
    const ids = new Set(hydrateCalls.map((c) => c.id));
    const results = [];
    for (let j = i + 1; j < messages.length; j++) {
      const t = messages[j];
      if (t.role !== 'tool') break;
      if (ids.has(t.tool_call_id)) results.push(t);
    }
    if (results.length > 0) return { assistant: m, results, calls: hydrateCalls };
  }
  return null;
}

// hydrationSlotNames returns the tool-call function names from a hydration
// exchange, in order. Used to assert the dynamic transcript is present.
function hydrationSlotNames(hydration) {
  return hydration.calls.map((c) => c.function?.name);
}

// assertUserBeforeHydration pins OpenAI/Claude message order: after any
// system prompt, the first user must precede the hydration assistant+tools.
function assertUserBeforeHydration(messages, label) {
  let firstUser = -1;
  let hydAsst = -1;
  for (let i = 0; i < messages.length; i++) {
    const m = messages[i];
    if (firstUser < 0 && m.role === 'user') firstUser = i;
    if (hydAsst < 0 && m.role === 'assistant' && Array.isArray(m.tool_calls)
        && m.tool_calls.some((c) => c.id?.startsWith('hydrate-'))) {
      hydAsst = i;
    }
  }
  assert.ok(firstUser >= 0, `${label}: request must contain a user message`);
  assert.ok(hydAsst >= 0, `${label}: request must contain the hydration assistant`);
  assert.ok(
    hydAsst > firstUser,
    `${label}: hydration assistant at index ${hydAsst} must follow first user at ${firstUser}`,
  );
}

// HYDR-NEW-ROOM: A brand-new conversation's first turn must inject the
// runtime-hydration transcript (dynamic: only slots with real content) into
// the provider request. The transcript is ephemeral — never persisted — so
// it must appear in the request body but NOT in the persisted conversation
// messages.
test('HYDR-NEW-ROOM: first turn of a new conversation injects the hydration transcript', async (t) => {
  // Large context: the hydration checkpoint itself is what this test
  // inspects, so it must not trip pre-turn compaction on a 1k window.
  const { llm, rpcModule, dataDir, server } = await startE2EHarness(t, {
    prefix: 'nusashell-hydr-new-room',
    llmOptions: { contextLength: 200000 },
  });

  try {
    // Seed both always-injected documents so hydration must persist two
    // separate file_read call/result pairs.
    await mkdir(join(dataDir, 'memory'), { recursive: true });
    await writeFile(join(dataDir, 'memory', 'user.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [frag_test] User prefers concise answers.\n');
    await writeFile(join(dataDir, 'memory', 'soul.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [soul_test] Soul keeps the tool transcript explicit.\n');

    // Create a new conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Script: a single short reply so the turn finishes quickly.
    llm.setScripts([[{ text: 'Hello from the assistant.' }]]);

    // Start the first turn.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'hi', model: 'tiny-model',
    });

    // Wait for the turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'first turn done', 15000);

    // Verify the streaming request contains the hydration transcript.
    const streamingReqs = llm.requests().filter((r) => r.stream);
    assert.ok(streamingReqs.length > 0, 'at least one streaming request must have been sent');
    const lastStream = streamingReqs[streamingReqs.length - 1];
    const hydration = findHydration(lastStream.body.messages);
    assert.ok(hydration, 'HYDR-NEW-ROOM: hydration transcript must be present in the first turn request');
    assertUserBeforeHydration(lastStream.body.messages, 'HYDR-NEW-ROOM');

    // Dynamic transcript: this harness has no plugins and no todos, so the
    // mcp_list / tool_list / todo_list slots are hidden. The seeded user
    // document and embedded skill library keep file_read + skill alive.
    const slots = hydrationSlotNames(hydration);
    assert.deepEqual(
      slots,
      ['runtime_context', 'file_list', 'file_read', 'file_read', 'skill'],
      `HYDR-NEW-ROOM: hydration slots must be the dynamic transcript in order, got ${JSON.stringify(slots)}`,
    );

    // The user document must be represented by a direct file_read result.
    const userCall = hydration.calls.find((c) => {
      if (c.function?.name !== 'file_read') return false;
      try { return JSON.parse(c.function.arguments || '{}').path === join(dataDir, 'memory', 'user.md'); } catch { return false; }
    });
    const userResult = hydration.results.find((r) => r.tool_call_id === userCall?.id);
    assert.ok(userCall, 'HYDR-NEW-ROOM: user file_read call must exist');
    assert.ok(userResult, 'HYDR-NEW-ROOM: user file_read tool result must exist');
    assert.ok(
      userResult.content.includes('User prefers concise answers.'),
      `HYDR-NEW-ROOM: user file_read result must contain the seeded entry, got: ${userResult.content}`,
    );
    const soulCall = hydration.calls.find((c) => {
      if (c.function?.name !== 'file_read') return false;
      try { return JSON.parse(c.function.arguments || '{}').path === join(dataDir, 'memory', 'soul.md'); } catch { return false; }
    });
    const soulResult = hydration.results.find((r) => r.tool_call_id === soulCall?.id);
    assert.ok(soulCall, 'HYDR-NEW-ROOM: soul file_read call must exist');
    assert.ok(soulResult, 'HYDR-NEW-ROOM: soul file_read tool result must exist');
    assert.ok(
      soulResult.content.includes('Soul keeps the tool transcript explicit.'),
      `HYDR-NEW-ROOM: soul file_read result must contain the seeded entry, got: ${soulResult.content}`,
    );

    // The hydration transcript is persisted to the conversation store for
    // prompt-cache stability but must be hidden from the UI (filtered out
    // of the conversations.get response).
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const visibleHydration = findHydration(
      (gotten.messages || []).map((m) => ({
        role: m.role,
        tool_calls: m.tool_calls,
        tool_call_id: m.tool_call_id,
      })),
    );
    assert.equal(
      visibleHydration, null,
      'HYDR-NEW-ROOM: hydration transcript must be hidden from the UI (not present in conversations.get response)',
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});

// HYDR-POST-COMPACTION: After compaction runs, the next turn must re-inject
// the hydration transcript. Compaction replaces durable history with a
// summary; the model loses all runtime facts (date, workspace, memory,
// skills, MCP catalog, tool catalog) unless hydration is re-injected.
// This test seeds enough history to trigger compaction, then verifies the
// post-compaction streaming request contains a fresh hydration transcript.
test('HYDR-POST-COMPACTION: turn after compaction re-injects the hydration transcript', async (t) => {
  const { llm, rpcModule, dataDir, server } = await startE2EHarness(t, {
    prefix: 'nusashell-hydr-post-compaction',
  });

  try {
    // Seed both always-injected documents so the post-compaction hydration
    // proves that each one is re-read through its own file_read call.
    await mkdir(join(dataDir, 'memory'), { recursive: true });
    await writeFile(join(dataDir, 'memory', 'user.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [frag_test] User is testing compaction hydration.\n');
    await writeFile(join(dataDir, 'memory', 'soul.md'), '---\nlast_updated: 2026-08-19T12:00:00Z\nversion: 1\n---\n\n- [soul_test] Soul survives compaction hydration.\n');

    // Disable compaction while seeding history.
    await rpcModule.rpc('settings.set', { compaction_enabled: false });

    // Create a conversation.
    window.location.hash = '#agent';
    window.dispatchEvent(new window.Event('hashchange'));
    document.getElementById('new-conversation-btn').click();
    await waitFor(
      () => [...document.querySelectorAll('#conversation-list .agent-conversation-title')].some((n) => n.textContent === 'Untitled'),
      'new conversation through the UI',
    );
    const conversations = await rpcModule.rpc('agent.conversations.list');
    const convID = conversations.conversations[0].id;

    // Seed 4 turns with large messages (~4000 tokens total) to exceed the
    // compaction trigger once it is enabled.
    const bigMsg = 'x'.repeat(2000);
    for (let i = 0; i < 4; i++) {
      llm.setScripts([[{ text: bigMsg }]]);
      await startTurn(rpcModule.rpc, {
        conversation_id: convID, text: bigMsg, model: 'tiny-model',
      });
      await waitFor(async () => {
        const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
        return c.conversation?.status === 'idle';
      }, `seed turn ${i + 1} done`, 15000);
    }

    // Clear captured requests so we only inspect the post-compaction turn.
    llm.requests().length = 0;

    // Enable compaction with a small context window (trigger = 800 tokens).
    // The seeded history (~4000 tokens) is well above the trigger.
    await rpcModule.rpc('settings.set', { max_input_tokens: 1000, compaction_enabled: true });

    // Set the compaction summary response (non-streaming).
    llm.setComplete(LONG_COMPACTION_SUMMARY);
    // Set the streaming reply for the post-compaction turn.
    llm.setScripts([[{ text: 'Turn after compaction with hydration.' }]]);

    // Trigger compaction + the next turn with a new user message.
    await startTurn(rpcModule.rpc, {
      conversation_id: convID, text: 'continue', model: 'tiny-model',
    });

    // Wait for the compaction marker to appear in the UI.
    await waitFor(
      () => [...document.querySelectorAll('#agent-thread .agent-compaction-marker')].some(
        (n) => n.textContent.includes('Compacted'),
      ),
      'compaction marker in the UI',
      15000,
    );

    // Wait for the post-compaction turn to finish.
    await waitFor(async () => {
      const c = await rpcModule.rpc('agent.conversations.get', { id: convID });
      return c.conversation?.status === 'idle';
    }, 'post-compaction turn done', 15000);

    // Verify the post-compaction streaming request contains a fresh
    // hydration transcript. The non-streaming compaction request must NOT
    // contain hydration (it summarizes durable history only).
    //
    // Filter out autolearn background-review requests: compaction triggers
    // a fire-and-forget learning review (subscribeCompactionReview) that
    // streams to the same fake LLM. The review agent sends its own system
    // prompt plus a synthetic transcript (tool call ids prefixed
    // "synthetic_"), and its request can land after the main turn's —
    // masking the post-compaction request we need to inspect.
    const allReqs = llm.requests();
    const nonStreamReqs = allReqs.filter((r) => !r.stream);
    const isAutolearnReview = (r) => {
      const sys = r.body.messages?.find((m) => m.role === 'system');
      if (typeof sys?.content === 'string' && sys.content.includes('background review agent')) return true;
      return (r.body.messages || []).some((m) =>
        Array.isArray(m.tool_calls) && m.tool_calls.some((c) => c.id?.startsWith('synthetic_')));
    };
    const streamReqs = allReqs.filter((r) => r.stream && !isAutolearnReview(r));

    // Non-streaming compaction request must not carry hydration.
    for (const ns of nonStreamReqs) {
      const nsHyd = findHydration(ns.body.messages);
      assert.equal(
        nsHyd, null,
        'HYDR-POST-COMPACTION: compaction summary request must not contain hydration transcript',
      );
    }

    // The streaming post-compaction request must carry hydration.
    assert.ok(streamReqs.length > 0, 'a streaming request must have been sent after compaction');
    const lastStream = streamReqs[streamReqs.length - 1];
    const hydration = findHydration(lastStream.body.messages);
    assert.ok(
      hydration,
      'HYDR-POST-COMPACTION: hydration transcript must be re-injected after compaction',
    );
    assertUserBeforeHydration(lastStream.body.messages, 'HYDR-POST-COMPACTION');

    // Dynamic transcript (see HYDR-NEW-ROOM): mcp/tool/todo slots are
    // hidden in this harness; user file_read + skill survive.
    const slots = hydrationSlotNames(hydration);
    assert.deepEqual(
      slots,
      ['runtime_context', 'file_list', 'file_read', 'file_read', 'skill'],
      `HYDR-POST-COMPACTION: hydration slots must be the dynamic transcript in order, got ${JSON.stringify(slots)}`,
    );

    // The user document must be represented by a fresh direct file_read
    // result (proves the transcript was rebuilt from live stores, not reused
    // from before compaction).
    const userCall = hydration.calls.find((c) => {
      if (c.function?.name !== 'file_read') return false;
      try { return JSON.parse(c.function.arguments || '{}').path === join(dataDir, 'memory', 'user.md'); } catch { return false; }
    });
    const userResult = hydration.results.find((r) => r.tool_call_id === userCall?.id);
    assert.ok(userCall, 'HYDR-POST-COMPACTION: user file_read call must exist');
    assert.ok(userResult, 'HYDR-POST-COMPACTION: user file_read tool result must exist');
    assert.ok(
      userResult.content.includes('User is testing compaction hydration.'),
      `HYDR-POST-COMPACTION: user file_read result must contain the seeded entry, got: ${userResult.content}`,
    );
    const soulCall = hydration.calls.find((c) => {
      if (c.function?.name !== 'file_read') return false;
      try { return JSON.parse(c.function.arguments || '{}').path === join(dataDir, 'memory', 'soul.md'); } catch { return false; }
    });
    const soulResult = hydration.results.find((r) => r.tool_call_id === soulCall?.id);
    assert.ok(soulCall, 'HYDR-POST-COMPACTION: soul file_read call must exist');
    assert.ok(soulResult, 'HYDR-POST-COMPACTION: soul file_read tool result must exist');
    assert.ok(
      soulResult.content.includes('Soul survives compaction hydration.'),
      `HYDR-POST-COMPACTION: soul file_read result must contain the seeded entry, got: ${soulResult.content}`,
    );

    // The hydration transcript is persisted to the conversation store for
    // prompt-cache stability but must be hidden from the UI (filtered out
    // of the conversations.get response).
    const gotten = await rpcModule.rpc('agent.conversations.get', { id: convID });
    const visibleHydration = findHydration(
      (gotten.messages || []).map((m) => ({
        role: m.role,
        tool_calls: m.tool_calls,
        tool_call_id: m.tool_call_id,
      })),
    );
    assert.equal(
      visibleHydration, null,
      'HYDR-POST-COMPACTION: hydration transcript must be hidden from the UI (not present in conversations.get response)',
    );
  } catch (error) {
    throw new Error(`${error.message}\nGo server output:\n${server.output()}`);
  }
});
