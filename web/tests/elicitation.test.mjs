// The elicitation panel draws only the fields its schema declares. A schema it
// cannot draw is only its message and a decline: no input is invented for a field
// the schema never asked for, and no Submit answers a form the panel cannot show.
import './env.mjs';
import { installDOM, doc } from './dom.mjs';
installDOM();
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { html, render } from '/vendor/preact/htm-preact.mjs';
import { ElicitationCard, drawnFields } from '../components/elicitation.js';

const FANCY = { type: 'object', properties: { pick: { type: 'fancypicker', title: 'Pick a colour' } }, required: ['pick'] };
const KNOWN = {
  type: 'object',
  properties: { name: { type: 'string', title: 'Name' }, agree: { type: 'boolean', title: 'Agree' } },
  required: ['name'],
};

// mount renders the card for one pending request and returns its host.
const mount = (schema) => {
  const host = doc.createElement('div');
  doc.body.appendChild(host);
  const seg = { status: 'pending', answering: '', request: { id: 'r1', message: 'Which one?', schema } };
  render(html`<${ElicitationCard} room=${{ id: 'c_eli' }} seg=${seg} />`, host);
  return host;
};
const inputs = (host) => host.querySelectorAll('input');
const buttons = (host) => host.querySelectorAll('button').map((b) => b.textContent.trim()).filter(Boolean);

test('the schema decides what is drawn: a field of an unknown type is not drawn as a guess', () => {
  assert.equal(drawnFields(FANCY), null);
  assert.deepEqual(drawnFields(KNOWN).map(([k]) => k), ['name', 'agree']);
  assert.equal(drawnFields(undefined), null, 'a request without a schema draws nothing');
  assert.deepEqual(drawnFields({ type: 'object', properties: {} }), [], 'an object with no fields is drawn, with none');
});

test('an unknown schema draws no input and no Submit: the message, and decline', () => {
  const host = mount(FANCY);
  assert.equal(inputs(host).length, 0, 'no input for a field the schema never asked for');
  assert.deepEqual(buttons(host), ['Decline'], 'decline is the one answer');
  render(null, host);
});

test('a known schema draws exactly its declared fields, and Submit answers them', () => {
  const host = mount(KNOWN);
  assert.deepEqual(inputs(host).map((i) => i.getAttribute('id')), ['eli-r1-name']);
  assert.deepEqual(buttons(host), ['Decline', 'Submit']);
  render(null, host);
});
