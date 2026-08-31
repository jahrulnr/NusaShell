import assert from 'node:assert/strict';
import { test } from 'node:test';
import { JSDOM } from 'jsdom';

import { renderSubagentCard } from '../js/views/agent/render.js';

test('completed subagent cards retain the run id from their brief result', () => {
  const dom = new JSDOM('<main id="thread"></main>');
  const previousDocument = globalThis.document;
  globalThis.document = dom.window.document;
  try {
    const card = renderSubagentCard({
      name: 'subagent',
      args: { prompt: 'Inspect the CSS', agent_id: 'acp_dev' },
      status: 'ok',
      output: 'Subagent run acprun_historic123 completed. Full result delivered in the subagent_result tool call.',
    });
    assert.equal(card.dataset.runId, 'acprun_historic123');
    assert.equal(card.getAttribute('role'), 'button');
  } finally {
    globalThis.document = previousDocument;
    dom.window.close();
  }
});
