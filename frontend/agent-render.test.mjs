import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderConversation, renderEmptyThread, renderToolJob, renderToolCallCard, appendToolJobDelta, appendLiveError, bindToolStop, renderMessageAttachments, parseShowAudioOutput, parseShowVideoOutput, STARTER_PROMPTS, reasoningDisclosure, mountLiveRound, sealLiveNodeBeforeSteer, insertAfterOrAppend } from './js/views/agent/render.js';
function renderTranscript(messages) {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    thread.append(renderConversation(messages));
    return thread;
  } finally {
    globalThis.document = previousDocument;
  }
}

// exec tool output persisted on the conversation is rendered verbatim when
// the thread is reloaded from the snapshot (no live run in memory), so users
// see the streamed output after a refresh / room switch.
test('exec tool output is rendered from the persisted conversation', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 5' }, status: 'ok',
            output: 'exit_code: 0\nduration_ms: 5000\n---\nPING reply 1\nPING reply 2\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-terminal');
  assert.ok(terminal, 'exec card rendered from snapshot');
  assert.equal(terminal.classList.contains('is-success'), true, 'card is success');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /PING reply 1/);
  assert.match(out.textContent, /PING reply 2/);
  // The streaming-only Stop button must not survive the reload.
  assert.equal(terminal.querySelector('.agent-tool-stop').hidden, true);
});

// exec tool with interrupted status keeps the persisted partial output so
// users see the streamed lines after a reload, not just "interrupted by user".
test('exec tool with interrupted status renders persisted partial output', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Cancel a long command', created_at: '2026-08-25T00:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-25T00:00:01Z',
      steps: [
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'exec', args: { command: 'sleep 60' }, status: 'interrupted',
            output: 'error: exec cancelled: context canceled\npartial output:\nfirst chunk\nsecond chunk\n' },
        ] },
      ],
    },
  ]);
  const terminal = thread.querySelector('.agent-tool-terminal');
  assert.equal(terminal.classList.contains('is-error'), true, 'interrupted status flagged as error styling');
  const out = terminal.querySelector('.agent-tool-terminal-output');
  assert.match(out.textContent, /first chunk/);
  assert.match(out.textContent, /second chunk/);
});

test('compaction summary renders above retained chronological turns, not after them', () => {
  const thread = renderTranscript([
    { role: 'user', content: '[COMPACTION CHECKPOINT]\nGoal: fix ordering. Done: found Compact regrouped users then assistants.', created_at: '2026-08-27T15:50:00Z' },
    { role: 'user', content: 'keep going', created_at: '2026-08-27T15:51:00Z' },
    { role: 'assistant', content: 'live delta continues here', created_at: '2026-08-27T15:52:00Z' },
  ]);
  const nodes = [...thread.children];
  assert.equal(nodes[0].classList.contains('agent-compaction-marker'), true, 'handover is the first live bubble');
  assert.match(nodes[0].textContent, /Compacted context/);
  assert.equal(nodes[1].classList.contains('user'), true);
  assert.match(nodes[1].textContent, /keep going/);
  assert.equal(nodes[2].classList.contains('assistant'), true);
  assert.match(nodes[2].textContent, /live delta continues here/);
});

test('renders one model and usage summary for all assistant rounds in a user turn', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run the checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 100, output_tokens: 20 },
      steps: [
        { type: 'reasoning', content: 'I will inspect the workspace.' },
        { type: 'tool_calls', tool_calls: [
          { id: 'tool_1', name: 'memory_list', args: {}, status: 'ok', output: '[]' },
          { id: 'tool_2', name: 'memory_search', args: {}, status: 'ok', output: '[]' },
        ] },
      ],
    },
    {
      role: 'assistant', model: 'deepseek', created_at: '2026-08-13T18:00:02Z',
      usage: { input_tokens: 200, output_tokens: 40, cache_read: 8 },
      steps: [{ type: 'text', content: 'The checks are complete.' }],
    },
    { role: 'user', content: 'Do one more thing', created_at: '2026-08-13T18:01:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:01:01Z',
      usage: { input_tokens: 50, output_tokens: 10 },
      steps: [{ type: 'tool_calls', tool_calls: [{ id: 'tool_3', name: 'skill', args: { op: 'list' }, status: 'ok', output: '[]' }] }],
    },
  ]);

  const assistantTurns = thread.querySelectorAll('.agent-message.assistant');
  assert.equal(assistantTurns.length, 2);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-turn-meta').length, 1);
  assert.equal(assistantTurns[0].querySelectorAll('.agent-tool-terminal').length, 2);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /deepseek/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /↑300 ↓60/);
  assert.match(assistantTurns[0].querySelector('.agent-turn-meta').textContent, /cache 8/);
  assert.match(assistantTurns[1].querySelector('.agent-turn-meta').textContent, /luna/);
});

test('usage badges render compact token units instead of raw counts', () => {
  const thread = renderTranscript([
    { role: 'user', content: 'Run checks', created_at: '2026-08-13T18:00:00Z' },
    {
      role: 'assistant', model: 'luna', created_at: '2026-08-13T18:00:01Z',
      usage: { input_tokens: 27085560, output_tokens: 137158, cache_read: 25944438 },
      steps: [{ type: 'text', content: 'done' }],
    },
  ]);
  const meta = thread.querySelector('.agent-turn-meta').textContent;
  assert.match(meta, /↑27\.09M ↓137\.16k/);
  assert.match(meta, /cache 25\.94M/);
  assert.doesNotMatch(meta, /27085560|137158|25944438/);
});

test('tool job summary includes elapsed span before chevron', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 'c1', name: 'exec', args: { command: 'ls' }, status: 'running' });
    const summary = job.querySelector('summary');
    const classes = [...summary.children].map((node) => node.className);
    const elapsedIdx = classes.indexOf('agent-tool-elapsed');
    const chevronIdx = classes.indexOf('agent-tool-terminal-chevron');
    assert.ok(elapsedIdx >= 0, 'elapsed span present');
    assert.ok(chevronIdx >= 0, 'chevron span present');
    assert.ok(elapsedIdx < chevronIdx, 'elapsed sits left of chevron');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('empty thread renders starter chips that fill the composer', () => {
  const dom = new JSDOM(`
    <div id="agent-thread"></div>
    <div id="tool-job-strip"></div>
    <div id="agent-todo-strip"></div>
    <textarea id="composer-input"></textarea>
  `);
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    assert.ok(STARTER_PROMPTS.length >= 3);
    renderEmptyThread();
    const chips = [...document.querySelectorAll('[data-starter-prompt]')];
    assert.equal(chips.length, STARTER_PROMPTS.length);
    chips[0].click();
    assert.equal(document.getElementById('composer-input').value, STARTER_PROMPTS[0].prompt);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('generate_image renders a proof card instead of a tool terminal', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a red harbor boat' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genimage-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Developing|Emulsion|a red harbor boat/);

    const done = renderToolCallCard({
      id: 'tc1',
      name: 'generate_image',
      args: { prompt: 'a red harbor boat' },
      status: 'ok',
      output: '---\nstatus: completed\nprovider: openai\nmodel: gpt-image-1\nsize: 1024x1024\ncost_usd: 0.04\nfile_path: /tmp/gen-tc1.png\n---\nImage saved.',
      output_attachments: [{ type: 'image', name: 'gen-tc1.png', media_type: 'image/png', file_path: '/tmp/gen-tc1.png' }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const img = done.querySelector('img');
    assert.ok(img);
    assert.match(img.getAttribute('src'), /\/local-file\?path=/);
    assert.match(done.textContent, /gpt-image-1/);
    assert.match(done.textContent, /Download/);
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
    assert.ok(done.querySelector('.agent-genimage-open'));

    const failed = renderToolCallCard({
      id: 'tc1', name: 'generate_image', args: { prompt: 'a boat' }, status: 'fail',
      output: 'error: No image generation model is configured. Ask the user to pick an image model in Settings → Image generation.',
    });
    assert.equal(failed.classList.contains('is-error'), true);
    assert.match(failed.textContent, /Settings/);
    assert.equal(failed.querySelectorAll('img').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

// Audio attachments returned by generate_speech (and any other tool that
// delivers persisted + inline audio) MUST render a playable <audio> element,
// not the generic TXT chip that image/video renderers explicitly avoid.
// Falls back to /local-file?path= when the inline data URL is too large to
// pass through history; inline wins when present so the chat thread can play
// audio without an extra HTTP round trip.
test('renderMessageAttachments renders an <audio> element for audio attachments', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const gallery = renderMessageAttachments([
      {
        type: 'audio',
        name: 'speech_20260825.wav',
        media_type: 'audio/wav',
        file_path: '/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav',
        data_url: 'data:audio/wav;base64,UklGRiQ=',
      },
    ]);
    const audio = gallery.querySelector('audio');
    assert.ok(audio, 'audio attachment renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    assert.equal(audio.getAttribute('preload'), 'metadata');
    // inline data URL wins so playback works even if /local-file is not yet
    // resolvable (e.g. before the server has flushed the attachment).
    assert.equal(audio.getAttribute('src'), 'data:audio/wav;base64,UklGRiQ=');
    const fig = audio.closest('figure');
    assert.ok(fig, 'audio is wrapped in a figure for layout parity with image/video');
    assert.ok(fig.classList.contains('agent-message-audio'), 'audio figure carries the audio layout class');
    assert.match(fig.querySelector('figcaption').textContent, /speech_20260825\.wav/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('renderMessageAttachments falls back to /local-file when audio has no data_url', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const gallery = renderMessageAttachments([
      {
        type: 'audio',
        name: 'speech_20260825.wav',
        media_type: 'audio/wav',
        file_path: '/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav',
      },
    ]);
    const audio = gallery.querySelector('audio');
    assert.ok(audio);
    assert.equal(
      audio.getAttribute('src'),
      '/local-file?path=' + encodeURIComponent('/home/user/.config/nusashell/attachments/conv_1/speech_20260825.wav'),
      'audio src falls back to /local-file when inline data_url is missing',
    );
  } finally {
    globalThis.document = previousDocument;
  }
});

test('exec tool terminal renders a stop button only while running', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    assert.equal(running.classList.contains('is-running'), true);
    let stop = running.querySelector('.agent-tool-stop');
    assert.ok(stop, 'streaming exec card has a stop button');
    assert.equal(stop.hidden, false, 'stop button visible while running');

    const done = renderToolJob({ id: 't2', name: 'exec', args: { command: 'ls' }, status: 'ok', output: 'ok\n' });
    assert.equal(done.classList.contains('is-success'), true);
    assert.equal(done.querySelector('.agent-tool-stop').hidden, true, 'stop hidden after done');

    // Non-streaming tools never show a stop button.
    const other = renderToolJob({ id: 't3', name: 'grep', args: {}, status: 'running' });
    assert.equal(other.querySelector('.agent-tool-stop'), null);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('appendToolJobDelta accumulates streamed output and clears placeholder', () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'ping' }, status: 'running' });
    const output = job.querySelector('.agent-tool-terminal-output');
    // Streaming exec starts with the "waiting" placeholder.
    assert.match(output.textContent, /waiting/);
    appendToolJobDelta(job, 'PING 8.8.8.8 (8.8.8.8)\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n');
    appendToolJobDelta(job, '64 bytes from 8.8.8.8\n');
    assert.equal(output.textContent, 'PING 8.8.8.8 (8.8.8.8)\n64 bytes from 8.8.8.8\n');
    // Placeholder must not reappear.
    assert.doesNotMatch(output.textContent, /waiting/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('bindToolStop calls agent.turns.stop with the run id until satisfied', async () => {
  const dom = new JSDOM('<main id="thread"></main>', { url: 'http://localhost/' });
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  let stopped = 0;
  const calls = [];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, opts) => {
    calls.push({ url, body: opts?.body });
    stopped++;
    if (stopped === 1) {
      throw new Error('transport failed');
    }
    return { ok: true, json: async () => ({ result: { ok: true } }) };
  };
  try {
    const job = renderToolJob({ id: 't1', name: 'exec', args: { command: 'sleep 5' }, status: 'running' });
    bindToolStop(job, () => 'run-123');
    const stop = job.querySelector('.agent-tool-stop');
    assert.equal(stop.hidden, false);
    // First click: transport fails → button re-enabled.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stop.disabled, false);
    assert.equal(stop.textContent, '■ Stop');
    // Second click: succeeds → button is retired.
    stop.click();
    await new Promise((resolve) => setTimeout(resolve, 20));
    assert.equal(stopped, 2);
    assert.equal(stop.isConnected, false, 'stop button removed after successful stop');
    assert.ok(calls.every((c) => c.url.includes('agent/turns/stop')));
    const payload = JSON.parse(calls[1].body).payload;
    assert.equal(payload.run_id, 'run-123');
  } finally {
    globalThis.fetch = originalFetch;
    globalThis.document = previousDocument;
  }
});

// show(op="audio") parallels show(op="image): the backend returns the same
// wire shape { show: { type, src, path, name } } but with type="audio" and a
// data:audio/...;base64,... src. The frontend parser should match type
// === "audio" and the card should render an <audio controls> element with
// a Download link, mirroring renderShowImageCard.
test('parseShowAudioOutput matches show(op=audio) results', () => {
  const out = JSON.stringify({
    show: {
      type: 'audio',
      src: 'data:audio/wav;base64,UklGRiQ=',
      path: '/tmp/speech.wav',
      name: 'speech.wav',
    },
  });
  assert.deepEqual(
    parseShowAudioOutput({ name: 'show', output: out }),
    { src: 'data:audio/wav;base64,UklGRiQ=', path: '/tmp/speech.wav', name: 'speech.wav' },
  );
  // Other tool names are ignored.
  assert.equal(parseShowAudioOutput({ name: 'generate_speech', output: out }), null);
  // Non-audio show results are ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: JSON.stringify({ show: { type: 'image', src: 'x' } }) }), null);
  // Unparseable output is ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: 'not json' }), null);
  // Missing output is ignored.
  assert.equal(parseShowAudioOutput({ name: 'show', output: '' }), null);
});

test('renderToolCallCard renders an audio card for show(op=audio)', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolCallCard({
      id: 'tc-audio',
      name: 'show',
      args: { op: 'audio', path: '/tmp/speech.wav' },
      status: 'ok',
      output: JSON.stringify({
        show: {
          type: 'audio',
          src: 'data:audio/wav;base64,UklGRiQ=',
          path: '/tmp/speech.wav',
          name: 'speech.wav',
        },
      }),
    });
    assert.equal(card.classList.contains('agent-genaudio-card'), true,
      'audio card carries its own class for parallel layout with image');
    const audio = card.querySelector('audio');
    assert.ok(audio, 'audio card renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    assert.equal(audio.getAttribute('preload'), 'metadata');
    assert.equal(audio.getAttribute('src'), 'data:audio/wav;base64,UklGRiQ=',
      'audio src is the inline data URL');
    assert.match(card.textContent, /speech\.wav/);
    assert.equal(card.querySelectorAll('.agent-tool-terminal').length, 0,
      'audio does NOT fall through to the generic tool terminal');
    // Download affordance matches the image card.
    assert.ok(card.querySelector('a[download]'), 'audio card exposes a Download link');
  } finally {
    globalThis.document = previousDocument;
  }
});

// generate_speech: tool-card path mirrors generate_image. The card should
// surface model/voice/duration/cost metadata parsed from the YAML output
// frontmatter and a Download link, so users have the same affordances they
// already get for images.
test('renderToolCallCard renders a speech card for generate_speech', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc-sp', name: 'generate_speech', args: { text: 'hello' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genaudio-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Synthesizing|hello/);

    const done = renderToolCallCard({
      id: 'tc-sp', name: 'generate_speech', args: { text: 'hello' }, status: 'ok',
      output: '---\nstatus: completed\nprovider: piper\nmodel: id_ID-news_tts-medium\nvoice: female\nmedia_type: audio/wav\nfile_path: /tmp/speech.wav\n---\nSpeech generated and saved to /tmp/speech.wav.',
      output_attachments: [{
        type: 'audio', name: 'speech.wav', media_type: 'audio/wav', file_path: '/tmp/speech.wav',
      }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const audio = done.querySelector('audio');
    assert.ok(audio, 'speech card renders an <audio> element');
    assert.equal(audio.getAttribute('controls'), '');
    // Inline data URL is preferred so playback works without an HTTP round trip.
    assert.match(audio.getAttribute('src') || '', /\/local-file\?path=/,
      'speech card falls back to /local-file when only file_path is available');
    assert.match(done.textContent, /piper|id_ID-news_tts-medium|female/);
    assert.ok(done.querySelector('a[download]'), 'speech card has a Download link');
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

// show(op="video") parallels show(op=audio): the backend returns the same
// wire shape { show: { type, src, path, name } } but with type="video" and
// a data:video/...;base64,... src. The frontend parser should match type
// === "video" and the card should render a <video controls> element with
// a Download link, mirroring renderShowImageCard.
test('parseShowVideoOutput matches show(op=video) results', () => {
  const out = JSON.stringify({
    show: {
      type: 'video',
      src: 'data:video/mp4;base64,AAAA',
      path: '/tmp/clip.mp4',
      name: 'clip.mp4',
    },
  });
  assert.deepEqual(
    parseShowVideoOutput({ name: 'show', output: out }),
    { src: 'data:video/mp4;base64,AAAA', path: '/tmp/clip.mp4', name: 'clip.mp4' },
  );
  // Other tool names are ignored.
  assert.equal(parseShowVideoOutput({ name: 'generate_video', output: out }), null);
  // Non-video show results are ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: JSON.stringify({ show: { type: 'image', src: 'x' } }) }), null);
  // Unparseable output is ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: 'not json' }), null);
  // Missing output is ignored.
  assert.equal(parseShowVideoOutput({ name: 'show', output: '' }), null);
});

test('renderToolCallCard renders a video card for show(op=video)', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderToolCallCard({
      id: 'tc-vid',
      name: 'show',
      args: { op: 'video', path: '/tmp/clip.mp4' },
      status: 'ok',
      output: JSON.stringify({
        show: {
          type: 'video',
          src: 'data:video/mp4;base64,AAAA',
          path: '/tmp/clip.mp4',
          name: 'clip.mp4',
        },
      }),
    });
    assert.equal(card.classList.contains('agent-genvideo-card'), true,
      'video card carries its own class for parallel layout with image/audio');
    const video = card.querySelector('video');
    assert.ok(video, 'video card renders a <video> element');
    assert.equal(video.getAttribute('controls'), '');
    assert.equal(video.getAttribute('preload'), 'metadata');
    assert.equal(video.getAttribute('src'), 'data:video/mp4;base64,AAAA',
      'video src is the inline data URL');
    assert.match(card.textContent, /clip\.mp4/);
    assert.equal(card.querySelectorAll('.agent-tool-terminal').length, 0,
      'video does NOT fall through to the generic tool terminal');
    // Download affordance matches the image/audio cards.
    assert.ok(card.querySelector('a[download]'), 'video card exposes a Download link');
  } finally {
    globalThis.document = previousDocument;
  }
});

// generate_video: tool-card path mirrors generate_image and generate_speech.
// Surfaces provider/model/duration/resolution/cost metadata parsed from
// the YAML output frontmatter plus a click-to-play <video> plate and a
// Download link.
test('renderToolCallCard renders a video card for generate_video', () => {
  const dom = new JSDOM('<main></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const running = renderToolCallCard({
      id: 'tc-gv', name: 'generate_video', args: { prompt: 'a cat' }, status: 'running',
    });
    assert.equal(running.classList.contains('agent-genvideo-card'), true);
    assert.equal(running.classList.contains('is-running'), true);
    assert.match(running.textContent, /Rendering|a cat/);

    const done = renderToolCallCard({
      id: 'tc-gv', name: 'generate_video', args: { prompt: 'a cat' }, status: 'ok',
      output: '---\nstatus: completed\nprovider: veo3\nmodel: veo-3\nmedia_type: video/mp4\nfile_path: /tmp/clip.mp4\n---\nVideo generated and saved to /tmp/clip.mp4.',
      output_attachments: [{
        type: 'video', name: 'clip.mp4', media_type: 'video/mp4', file_path: '/tmp/clip.mp4',
      }],
    });
    assert.equal(done.classList.contains('is-success'), true);
    const video = done.querySelector('video');
    assert.ok(video, 'video card renders a <video> element');
    assert.equal(video.getAttribute('controls'), '');
    assert.match(video.getAttribute('src') || '', /\/local-file\?path=/,
      'video card falls back to /local-file when only file_path is available');
    assert.match(done.textContent, /veo3|veo-3/);
    assert.ok(done.querySelector('a[download]'), 'video card has a Download link');
    assert.equal(done.querySelectorAll('.agent-tool-terminal').length, 0);
  } finally {
    globalThis.document = previousDocument;
  }
});

function openDetails(details) {
  const EventCtor = details.ownerDocument?.defaultView?.Event || Event;
  details.open = true;
  details.dispatchEvent(new EventCtor('toggle'));
}

test('reasoning markdown stays out of the DOM until the disclosure is opened', () => {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const details = reasoningDisclosure('I will inspect **the workspace**.');
    document.body.append(details);
    const content = details.querySelector('.agent-reasoning-content');
    assert.equal(details.hidden, false, 'non-empty reasoning is visible as a collapsed row');
    assert.equal(content.innerHTML, '', 'collapsed reasoning is not markdown-parsed');
    assert.doesNotMatch(details.textContent, /the workspace/, 'raw reasoning is not in the collapsed DOM');
    openDetails(details);
    assert.match(content.innerHTML, /<strong>the workspace<\/strong>/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('whitespace-only reasoning stays hidden and unparsed', () => {
  const dom = new JSDOM('<body></body>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const details = reasoningDisclosure('  \n\t');
    document.body.append(details);
    assert.equal(details.hidden, true);
    assert.equal(details.querySelector('.agent-reasoning-content').innerHTML, '');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('snapshot renderConversation keeps the user bubble and every round it is given (no earlier-rounds stub)', () => {
  const messages = [{ role: 'user', content: 'go', created_at: '2026-08-28T00:00:00Z' }];
  for (let i = 0; i < 6; i++) {
    messages.push({
      role: 'assistant',
      id: `msg_round_${i}`,
      model: 'luna',
      created_at: `2026-08-28T00:00:0${i + 1}Z`,
      steps: [
        { type: 'reasoning', content: `thinking UNIQUE_ROUND_${i}` },
        { type: 'text', content: `visible round ${i}` },
        { type: 'tool_calls', tool_calls: [{ id: `t${i}`, name: 'file_read', args: {}, status: 'ok', output: `out ${i}` }] },
      ],
    });
  }
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    thread.append(renderConversation(messages));
    assert.ok(thread.querySelector('.agent-message.user'));
    const turn = thread.querySelector('.agent-message.assistant');
    assert.equal(turn.querySelector('.agent-round-stub'), null);
    assert.equal(turn.querySelectorAll(':scope > .agent-bubble > .agent-round').length, 6);
    assert.match(turn.textContent, /visible round 0/);
    assert.match(turn.textContent, /visible round 5/);
    assert.doesNotMatch(turn.textContent, /UNIQUE_ROUND_5/, 'kept-round reasoning is still lazy');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('mountLiveRound keeps every live round mounted (no trim, no stub)', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    let current;
    for (let i = 0; i < 6; i++) {
      current = mountLiveRound(bubble, { rawReasoning: `think ${i}` });
    }
    // The hide-bubble workaround is gone: live deltas must never remove
    // earlier rounds from the DOM nor replace them with a stub.
    assert.equal(bubble.querySelector(':scope > .agent-round-stub'), null);
    const rounds = bubble.querySelectorAll(':scope > .agent-round');
    assert.equal(rounds.length, 6);
    for (const round of rounds) assert.ok(round.isConnected, 'round stays mounted');
    assert.equal(rounds[0].querySelector('.agent-reasoning')._reasoningRaw, 'think 0');
    assert.equal(bubble._liveRoundArchive, undefined, 'no parking archive');
    assert.ok(current.textBox.closest('.agent-round')?.isConnected, 'newest live round remains mounted');
  } finally {
    globalThis.document = previousDocument;
  }
});

test('sealing a live node before steer removes only empty thinking rounds', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-message assistant"><div class="agent-bubble"></div></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const node = document.querySelector('.agent-message.assistant');
    const bubble = node.querySelector('.agent-bubble');
    const settled = mountLiveRound(bubble, {});
    settled.textBox.textContent = 'Keep this completed round';
    const empty = mountLiveRound(bubble, {});
    empty.textBox.append(document.createElement('span'));
    empty.textBox.firstElementChild.className = 'agent-thinking-dots';

    sealLiveNodeBeforeSteer(node);

    assert.equal(node.querySelectorAll('.agent-thinking-dots').length, 0);
    assert.equal(node.querySelectorAll('.agent-round').length, 1);
    assert.match(node.textContent, /Keep this completed round/);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('a delayed live round inserts immediately after its steer anchor', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const thread = document.getElementById('thread');
    const prior = document.createElement('div');
    prior.dataset.order = 'prior';
    const steer = document.createElement('div');
    steer.dataset.order = 'steer';
    const later = document.createElement('div');
    later.dataset.order = 'later';
    const response = document.createElement('div');
    response.dataset.order = 'response';
    thread.append(prior, steer, later);

    insertAfterOrAppend(thread, response, steer);

    assert.deepEqual(
      [...thread.children].map((node) => node.dataset.order),
      ['prior', 'steer', 'response', 'later'],
    );
  } finally {
    globalThis.document = previousDocument;
  }
});

test('appendLiveError preserves streamed assistant content', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    const { textBox } = mountLiveRound(bubble, {});
    textBox.textContent = 'The streamed answer remains visible';
    appendLiveError(bubble, 'provider failed');
    assert.match(bubble.textContent, /The streamed answer remains visible/);
    assert.match(bubble.textContent, /provider failed/);
    assert.equal(bubble.querySelectorAll('.agent-live-error').length, 1);
  } finally {
    globalThis.document = previousDocument;
  }
});

test('live rounds stay mounted no matter how many arrive (performance is CSS + targeted enhancement, not DOM removal)', () => {
  const dom = new JSDOM('<main id="thread"><div class="agent-bubble"></div></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const bubble = document.querySelector('.agent-bubble');
    let current;
    for (let i = 0; i < 40; i++) {
      current = mountLiveRound(bubble, { raw: `output ${i}` });
    }
    assert.equal(bubble.querySelectorAll(':scope > .agent-round').length, 40);
    assert.equal(bubble.querySelector(':scope > .agent-round-stub'), null);
    assert.equal(bubble._liveRoundArchive, undefined);
    for (const round of bubble.querySelectorAll(':scope > .agent-round')) {
      assert.ok(round.isConnected, 'every round stays in the live DOM');
    }
    assert.ok(current.textBox.closest('.agent-round')?.isConnected);
  } finally {
    globalThis.document = previousDocument;
  }
});
