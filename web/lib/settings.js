// The settings document as a page edits it: one read when the page opens, a
// retry, and the document a write answers with, so the fields keep showing
// what the server holds instead of a stale copy.
import { useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { api, errorText } from './api.js';

// useSettingsDoc answers [doc, { reload, saved }]. doc is
// { status: 'loading' } | { status: 'error', error } | { status: 'ready', d }.
export function useSettingsDoc() {
  const [doc, setDoc] = useState({ status: 'loading' });
  const reload = () => {
    setDoc({ status: 'loading' });
    api.settings().then((d) => setDoc({ status: 'ready', d }), (e) => setDoc({ status: 'error', error: errorText(e) }));
  };
  useEffect(reload, []);
  return [doc, { reload, saved: (d) => setDoc({ status: 'ready', d }) }];
}

// getPath reads a dotted key of the settings document ('notifications.desktop').
export const getPath = (o, k) => k.split('.').reduce((a, p) => a?.[p], o);

// readingConsent answers the one switch behind read_media: whether a file a
// model asks for may be sent out of the app. An absent leaf is off, and only a
// JSON true turns it on — a string is not consent.
export const readingConsent = (d) => d?.media?.reading_consent === true;

// setReadingConsent writes that switch. The leaf is a boolean, and the server
// refuses the string "true" without changing anything, so the literal goes out
// as a boolean and never as a sentence.
export const setReadingConsent = (on) => api.setSettings({ 'media.reading_consent': on === true });

// roleLeaves answers the settings leaves a role may write — the one place the
// page decides that, for both the model picker and the effort select. A role
// without an effort has no such leaf, and writing one anyway would fail the
// whole atomic write: the server refuses a leaf it does not know, and the model
// picked in the same change would be lost with it.
export function roleLeaves(role) {
  const leaves = [`models.${role.id}.provider`, `models.${role.id}.model`];
  if (role.agents) leaves.push(`models.${role.id}.agent`);
  if (role.effort) leaves.push(`models.${role.id}.effort`);
  return leaves;
}

// rolePatch is what a model picker writes for its role: a provider and a
// model, an agent for the roles that take one, and an effort only for the
// roles that have one — an agent carries its own, and a role with no model
// cannot keep one.
export function rolePatch(role, next) {
  const leaves = roleLeaves(role);
  const vals = {};
  if (leaves.includes(`models.${role.id}.provider`)) vals[`models.${role.id}.provider`] = next.provider || '';
  if (leaves.includes(`models.${role.id}.model`)) vals[`models.${role.id}.model`] = next.model || '';
  if (leaves.includes(`models.${role.id}.agent`)) vals[`models.${role.id}.agent`] = next.agent || '';
  if (leaves.includes(`models.${role.id}.effort`) && (next.agent || !next.provider || !next.model)) {
    vals[`models.${role.id}.effort`] = '';
  }
  return vals;
}

// setRoleEffort writes the effort a thread of that role starts on. A role with
// no effort leaf answers null and writes nothing, so the select that offers it
// and the write it makes read the same rule.
export function setRoleEffort(role, effort) {
  const leaf = `models.${role.id}.effort`;
  if (!roleLeaves(role).includes(leaf)) return null;
  return api.setSettings({ [leaf]: effort || '' });
}
