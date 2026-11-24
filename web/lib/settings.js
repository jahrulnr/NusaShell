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
