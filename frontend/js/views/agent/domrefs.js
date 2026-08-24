// Cached element references for the agent view.
//
// The mini window (Document Picture-in-Picture, js/pip.js) MOVES
// #agent-thread and #agent-composer-stack into another document, so
// document.getElementById() in the shell returns null for them while the
// mini window is open. Caching the nodes once keeps every update path
// (streaming, scroll pinning, composer handlers) working on the moved
// elements; when the mini window closes they are moved back, and the
// same references remain valid.
//
// The cache is invalidated when the node's ownerDocument no longer
// matches the current global document. The e2e harness swaps
// globalThis.document per test while ESM modules stay cached in the
// process, so a stale node from a previous test must never be reused.

let threadEl = null;
let composerEl = null;
let formEl = null;
let inputEl = null;
let stripEl = null;
let sendEl = null;
let stopEl = null;
let attachContainerEl = null;
let workspaceBtnEl = null;

function cached(id, ref) {
  if (!ref || ref.ownerDocument !== document) {
    ref = document.getElementById(id);
  }
  return ref;
}

export function agentThread() {
  threadEl = cached('agent-thread', threadEl);
  return threadEl;
}

export function composerStack() {
  composerEl = cached('agent-composer-stack', composerEl);
  return composerEl;
}

export function agentForm() {
  formEl = cached('agent-form', formEl);
  return formEl;
}

export function composerInput() {
  inputEl = cached('composer-input', inputEl);
  return inputEl;
}

export function toolJobStrip() {
  stripEl = cached('tool-job-strip', stripEl);
  return stripEl;
}

export function sendButton() {
  sendEl = cached('send-btn', sendEl);
  return sendEl;
}

export function stopButton() {
  stopEl = cached('stop-btn', stopEl);
  return stopEl;
}

export function attachmentsContainer() {
  attachContainerEl = cached('agent-attachments', attachContainerEl);
  return attachContainerEl;
}

export function workspaceButton() {
  workspaceBtnEl = cached('agent-workspace-btn', workspaceBtnEl);
  return workspaceBtnEl;
}

export function providerStatus() {
  return document.getElementById('agent-provider-status');
}

export function workspaceLabel() {
  return document.getElementById('agent-workspace-label');
}
