// Settings → Models: what a new thread starts on, and which model does each
// job the app runs on its own — answering chat channels, small utility
// tasks, embeddings, media.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast, state, emit } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { readingConsent, rolePatch, setReadingConsent, setRoleEffort, useSettingsDoc } from '../lib/settings.js';
import { loadAgentModels } from '../lib/chat.js';
import { Icon } from './icons.js';
import { cx, ErrorBlock, Select, Spinner, Toggle } from './ui.js';

// kinds lists the model types a role can use. A text role also takes a model
// whose type is not declared, since an untyped model is a chat model until
// someone says otherwise; every other role takes only the models typed for
// it, so an embedding picker never offers a chat model.
export const ROLES = [
  { id: 'threads', icon: 'plus', title: 'New threads', agents: true, effort: true, kinds: ['text'],
    desc: 'What a new thread starts on, on every device. Picking another in the composer changes only that thread.',
    none: 'The last one picked in this browser' },
  { id: 'chat', icon: 'message', title: 'Chat channels', agents: true, effort: true, kinds: ['text'],
    desc: 'Answers the threads WhatsApp and Telegram start. An agent or a model, so replies come from the one you chose.',
    none: 'Automatic · first agent, then first provider' },
  { id: 'utility', icon: 'spark', title: 'Utility', kinds: ['text'],
    desc: 'Small jobs for the app: names a thread after its first reply. A fast, cheap model is enough.',
    none: 'Off' },
  { id: 'embedding', icon: 'layers', title: 'Embedding', kinds: ['embedding'], later: 'Memory uses it; not in use yet.',
    desc: 'Turns text into vectors so threads can recall what was said before.',
    none: 'Off' },
  { id: 'transcription', icon: 'mic', title: 'Speech to text', kinds: ['transcription'],
    desc: 'Turns speech into text: voice input in the composer, and when a thread transcribes an audio or video file.',
    none: 'Off' },
  // Vision reads an image for a thread whose own model cannot see it. Assigning
  // it sends nothing: media.reading_consent is the switch that decides whether
  // a file may leave the app at all.
  { id: 'vision', icon: 'eye', title: 'Vision', kinds: ['text'], needs: 'image',
    desc: 'Describes an image for a model that cannot see one. Picking it sends nothing on its own; the switch below decides whether a file may be sent.',
    none: 'Off' },
  { id: 'image', group: 'media', icon: 'image', title: 'Image', kinds: ['image'],
    desc: 'Makes images from text, or from text and images.', none: 'Off' },
  { id: 'video', group: 'media', icon: 'video', title: 'Video', kinds: ['video'],
    desc: 'Makes video from text, images, or sound.', none: 'Off' },
  { id: 'speech', group: 'media', icon: 'volume', title: 'Speech', kinds: ['speech'],
    desc: 'Reads text aloud: text to speech.', none: 'Off' },
];
const MEDIA = { title: 'Media generation', desc: 'Makes images, video, and speech when a thread asks for them. A thread’s model calls these through its media tools; a role left off turns its tool away with a note to set it here.' };

// EFFORTS lists every level the harness can send. A model that declares its
// own list narrows this; one that declares nothing keeps the full set, the
// same fallback the composer uses.
const EFFORTS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'];

// A role's value in a picker: '' (unassigned), 'a:<agent>', or
// 'p:<provider>\n<model>' — a model id may hold any character but a newline.
const encode = (r) => (r?.agent ? 'a:' + r.agent : r?.provider && r?.model ? `p:${r.provider}\n${r.model}` : '');
function decode(v) {
  if (v.startsWith('a:')) return { agent: v.slice(2), provider: '', model: '' };
  if (v.startsWith('p:')) { const [provider, model] = v.slice(2).split('\n'); return { agent: '', provider, model }; }
  return { agent: '', provider: '', model: '' };
}

export function Models() {
  const s = useStore();
  const [doc, { reload, saved }] = useSettingsDoc();
  const lists = s.providers.status === 'loading' || s.agents.status === 'loading';
  return html`<div>
    <div class="mb-4">
      <h2 class="text-[17px] font-semibold">Models</h2>
      <p class="text-muted text-[13px] mt-0.5">What new threads start on, and what runs the jobs the app does on its own.</p>
    </div>
    ${(doc.status === 'loading' || lists) && html`<div class="flex flex-col gap-2">${ROLES.map((r) => html`<div key=${r.id} class="skeleton h-[92px] rounded-xl"></div>`)}</div>`}
    ${doc.status === 'error' && html`<${ErrorBlock} title="Could not load settings" message=${doc.error} onRetry=${reload} />`}
    ${doc.status === 'ready' && !lists && html`<div class="flex flex-col gap-3">
      ${ROLES.filter((r) => !r.group).map((role) => html`<${RoleCard} key=${role.id} role=${role} value=${doc.d.models?.[role.id]}
        onSaved=${saved} />`)}
      <${ReadingConsent} d=${doc.d} onSaved=${saved} />
      <div class="mt-3 flex items-center gap-2">
        <h3 class="font-medium">${MEDIA.title}</h3>
        <span class="rounded px-1 border border-line text-[10px] leading-4 text-faint">Later</span>
      </div>
      <p class="text-muted text-[13px] -mt-2">${MEDIA.desc}</p>
      ${ROLES.filter((r) => r.group === 'media').map((role) => html`<${RoleCard} key=${role.id} role=${role} value=${doc.d.models?.[role.id]}
        onSaved=${saved} />`)}
    </div>`}
  </div>`;
}

// rolePicker answers the choices a role's select offers, with the role's
// current value kept selectable so the page never shows a blank it would save
// on the next render.
//
// A role's kinds are model types it can run on; needs names one input
// modality on top of them — a vision model is not a kind of its own, it is a
// text model that can look at an image. A model that exists but does not fit
// keeps its own name here: "(not found)" is for a model the catalog no longer
// lists, not for one this select filters out.
const KIND = (m) => m.modelType || '';
function roleFits(role, m) {
  const text = role.kinds.includes('text');
  const kind = role.kinds.includes(KIND(m)) || (text && !KIND(m));
  if (!kind) return false;
  return !role.needs || (m.inputModalities || []).includes(role.needs);
}

export function rolePicker(role, providers = [], agentList = [], value = null) {
  const cur = encode(value);
  const shown = (m) => !role.agents || m.listed !== false;
  const groups = providers.filter((p) => !p.broken).map((p) => ({
    label: `${p.name || p.id} · API`,
    options: p.models.filter((m) => shown(m) && roleFits(role, m)).map((m) => ({ value: `p:${p.id}\n${m.id}`, text: m.name || m.id })),
  })).filter((g) => g.options.length);
  const agents = role.agents && agentList.length
    ? [{ label: 'Agents · ACP', options: agentList.map((a) => ({ value: 'a:' + a.id, text: a.name || a.id })) }] : [];
  const options = [{ value: '', text: role.none }, ...agents, ...groups];
  const known = options.some((o) => o.value === cur || o.options?.some((x) => x.value === cur));
  if (!cur || known) return options;

  // What the role names, in the catalog's own words when the model is there:
  // a model assigned through the API still reads as itself, even when this
  // select is not the place to pick it.
  const found = providers.find((p) => p.id === value.provider)?.models.find((m) => m.id === value.model);
  const text = value.agent || `${found ? (providers.find((p) => p.id === value.provider)?.name || value.provider) : value.provider} · ${found ? (found.name || found.id) : value.model}`;
  return [...options, { value: cur, text: found ? text : `${text} (not found)` }];
}

// ReadingConsent is the one switch behind read_media. Both readings sit under
// it — an image description and an audio transcript are the same act, sending
// a file out of the app — so asking twice would only teach the owner to click
// through. It is off unless the server says true, and a refused write leaves
// the switch showing the value the server still holds, with the reason under
// it.
function ReadingConsent({ d, onSaved }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(null);
  const on = readingConsent(d);
  const vision = d?.models?.vision;
  const stt = d?.models?.transcription;
  const name = (r) => (r?.provider && r?.model ? `${r.provider} / ${r.model}` : '');
  const visionName = name(vision);
  const sttName = name(stt);
  const save = async (next) => {
    setBusy(true);
    setError(null);
    try {
      onSaved(await setReadingConsent(next));
      toast(next ? 'Files may be sent to a reading model.' : 'Files stay in the app.');
    } catch (e) { setError(errorText(e)); }
    setBusy(false);
  };
  // What is still missing before the switch can keep its promise. One consent
  // covers both readings, so each role it depends on is named on its own.
  const needs = [!visionName && 'vision', !sttName && 'transcription'].filter(Boolean);
  const note = needs.length === 2
    ? html`Needs a vision model above (and a transcription model for audio) — without one, <code class="font-mono text-[11px]">read_media</code> refuses instead of sending.`
    : needs[0] === 'vision'
      ? html`Needs a vision model above — without one, <code class="font-mono text-[11px]">read_media</code> refuses instead of sending.`
      : html`Needs a transcription model above — without one, <code class="font-mono text-[11px]">read_media</code> refuses audio instead of sending.`;
  return html`<section class="rounded-xl border border-line bg-panel px-4 py-3.5 flex flex-col gap-2.5">
    <div class="flex items-start gap-3">
      <span class="mt-0.5 size-8 shrink-0 rounded-lg bg-raised flex items-center justify-center text-muted"><${Icon} name="file" size=${16} /></span>
      <div class="flex-1 min-w-0">
        <span class="font-medium">Send files to a model that can read them</span>
        <p class="text-muted text-[13px] mt-0.5">When a thread’s model cannot read an image or hear audio, ${' '}
          <code class="font-mono text-[12px]">read_media</code> sends the file to ${' '}
          ${visionName ? html`<span class="font-mono text-[12px]">${visionName}</span>` : 'the vision model above'} for a description, or to the ${' '}
          transcription model ${sttName ? html`(<span class="font-mono text-[12px]">${sttName}</span>)` : '(none is set above)'} for a transcript.
          Off by default. Only a file the model explicitly asks for is sent; each thread caches its answers for itself and never shares them with another thread.</p>
      </div>
      <${Toggle} checked=${on} disabled=${busy} onChange=${save} label="Send files to a model that can read them" />
    </div>
    ${on && needs.length > 0 && html`<p class="text-xs text-warn sm:pl-11">${note}</p>`}
    ${error && html`<p role="alert" class="text-xs text-bad sm:pl-11">${error}</p>`}
  </section>`;
}

function RoleCard({ role, value, onSaved }) {
  const s = useStore();
  const [busy, setBusy] = useState(false);
  const cur = encode(value);
  const agentId = value?.agent || '';
  // A model declares the efforts it accepts; a model with no list keeps the
  // full set. The role's own choice is the thread's starting effort.
  const picked = value?.provider && value?.model
    ? s.providers.items.find((p) => p.id === value.provider)?.models.find((m) => m.id === value.model) : null;
  const efforts = picked?.supportedEfforts?.length ? picked.supportedEfforts : EFFORTS;
  const effort = value?.effort || '';
  const effortUnsupported = !!effort && !efforts.includes(effort);
  // Keep an effort the model no longer declares selectable, marked as such:
  // dropping it from the list would make the select fall back to the first
  // option and silently save that empty choice on render.
  const effortOptions = [
    { value: '', text: 'Provider default' },
    ...efforts.map((e) => ({ value: e, text: e[0].toUpperCase() + e.slice(1) })),
    ...(effortUnsupported ? [{ value: effort, text: effort[0].toUpperCase() + effort.slice(1) + ' (not declared)' }] : []),
  ];

  // Only the thread-opening roles take an effort: an agent carries its own
  // among its session options, and the app's own jobs keep the model default.
  const takesEffort = !!role.effort;
  const options = rolePicker(role, s.providers.items, s.agents.items, value);
  // What the picker actually offers, so the hint below can say which of the
  // two reasons it is: nothing to pick from, or nothing that fits this role.
  const hasProviders = s.providers.items.some((p) => !p.broken);
  const offeredModel = options.some((o) => o.options?.length);
  const offeredAgent = options.some((o) => typeof o.value === 'string' && o.value.startsWith('a:'));

  // name is what the toast calls the choice: the option's text, not its id.
  const save = async (next, name) => {
    setBusy(true);
    const vals = rolePatch(role, next);
    try {
      const d = await api.setSettings(vals);
      state.threadDefault = d.models?.threads || null;
      emit();
      onSaved(d);
      toast(`${role.title}: ${name}.`);
    } catch (e) { toast(errorText(e), 'error'); }
    setBusy(false);
  };

  // saveEffort writes the effort a thread of this role starts on; "" leaves
  // it to the model's own default, then the provider's.
  const saveEffort = async (v) => {
    const write = setRoleEffort(role, v);
    if (!write) return; // this role has no effort leaf, so the select is not offered
    setBusy(true);
    try {
      const d = await write;
      state.threadDefault = d.models?.threads || null;
      emit();
      onSaved(d);
      toast(`${role.title}: effort ${v || 'provider default'}.`);
    } catch (e) { toast(errorText(e), 'error'); }
    setBusy(false);
  };

  // An agent offers its models only once it runs; ask when one is picked.
  useEffect(() => { if (agentId) loadAgentModels(agentId); }, [agentId]);
  const am = agentId ? s.agentModels[agentId] : null;

  return html`<section class="rounded-xl border border-line bg-panel px-4 py-3.5 flex flex-col gap-3">
    <div class="flex items-start gap-3">
      <span class="mt-0.5 size-8 shrink-0 rounded-lg bg-raised flex items-center justify-center text-muted"><${Icon} name=${role.icon} size=${16} /></span>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <span class="font-medium">${role.title}</span>
          ${role.later && html`<span class="rounded px-1 border border-line text-[10px] leading-4 text-faint">Later</span>`}
          ${busy && html`<${Spinner} size=${12} />`}
        </div>
        <p class="text-muted text-[13px] mt-0.5">${role.desc}</p>
        ${role.later && html`<p class="text-faint text-xs mt-1">${role.later}</p>`}
      </div>
    </div>
    <div class="flex flex-col sm:flex-row gap-2 sm:pl-11">
      <${Select} label=${role.title} search class="flex-1 min-w-0" value=${cur} options=${options}
        onChange=${(v) => save(decode(v), options.flatMap((o) => o.options || [o]).find((o) => o.value === v)?.text ?? v)} />
      ${!agentId && takesEffort && html`<${Select} label=${role.title + ' effort'} class="sm:w-44" value=${effort}
        options=${effortOptions}
        onChange=${saveEffort} />`}
      ${agentId && html`<${Select} label=${role.title + ' agent model'} class=${cx('sm:w-56', am?.status === 'loading' && 'opacity-60')}
        value=${value.model || ''}
        options=${[{ value: '', text: am?.status === 'loading' ? 'Loading models…' : 'Agent’s default model' },
          ...(am?.list || []).map((m) => ({ value: m.id, text: m.name || m.id }))]}
        onChange=${(m) => save({ agent: agentId, provider: '', model: m }, (am?.list || []).find((x) => x.id === m)?.name || m || 'Agent’s default model')} />`}
    </div>
    ${!agentId && takesEffort && effortUnsupported && html`<p class="text-xs text-warn sm:pl-11">
      ${value.model} does not declare “${effort}”, so the provider may ignore it.
      <button type="button" class="underline" onClick=${() => saveEffort('')}>Use the provider default</button>.
    </p>`}
    ${!agentId && takesEffort && html`<p class="text-xs text-faint sm:pl-11">The effort list follows what the selected model declares. Reasoning models often do better at medium or higher.</p>`}
    ${am?.status === 'error' && html`<p class="text-xs text-bad sm:pl-11">The agent did not list its models: ${am.error}</p>`}
    ${!offeredModel && !offeredAgent && (hasProviders
      ? role.needs
        ? html`<p class="text-xs text-faint sm:pl-11">No model in this catalog takes an ${role.needs}. A provider’s catalog says what each model can read; the ones that can will appear here.</p>`
        : html`<p class="text-xs text-faint sm:pl-11">No model is marked as ${role.kinds[0]}. A provider’s catalog marks them when it can; otherwise set a model’s type under <a href="#/settings/providers" class="underline">Providers</a>.</p>`
      : html`<p class="text-xs text-faint sm:pl-11">Add a provider${role.agents ? ' or an agent' : ''} to choose one.</p>`)}
  </section>`;
}
