// Settings → Models: what a new thread starts on, and which model does each
// job the app runs on its own — answering chat channels, small utility
// tasks, embeddings, media.
import { html, useEffect, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast, state, emit } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { loadAgentModels } from '../lib/chat.js';
import { Icon } from './icons.js';
import { cx, ErrorBlock, Select, Spinner } from './ui.js';

// kinds lists the model types a role can use. A text role also takes a model
// whose type is not declared, since an untyped model is a chat model until
// someone says otherwise; every other role takes only the models typed for
// it, so an embedding picker never offers a chat model.
const ROLES = [
  { id: 'threads', icon: 'plus', title: 'New threads', agents: true, kinds: ['text'],
    desc: 'What a new thread starts on, on every device. Picking another in the composer changes only that thread.',
    none: 'The last one picked in this browser' },
  { id: 'chat', icon: 'message', title: 'Chat channels', agents: true, kinds: ['text'],
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
  { id: 'image', group: 'media', icon: 'image', title: 'Image', kinds: ['image'],
    desc: 'Makes images from text, or from text and images.', none: 'Off' },
  { id: 'video', group: 'media', icon: 'video', title: 'Video', kinds: ['video'],
    desc: 'Makes video from text, images, or sound.', none: 'Off' },
  { id: 'speech', group: 'media', icon: 'volume', title: 'Speech', kinds: ['speech'],
    desc: 'Reads text aloud: text to speech.', none: 'Off' },
];
const MEDIA = { title: 'Media generation', desc: 'Makes images, video, and speech when a thread asks for them. A thread’s model calls these through its media tools; a role left off turns its tool away with a note to set it here.' };

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
  const [doc, setDoc] = useState({ status: 'loading' });
  const load = () => {
    setDoc({ status: 'loading' });
    api.settings().then((d) => setDoc({ status: 'ready', d }), (e) => setDoc({ status: 'error', error: errorText(e) }));
  };
  useEffect(load, []);
  const lists = s.providers.status === 'loading' || s.agents.status === 'loading';
  return html`<div>
    <div class="mb-4">
      <h2 class="text-[17px] font-semibold">Models</h2>
      <p class="text-muted text-[13px] mt-0.5">What new threads start on, and what runs the jobs the app does on its own.</p>
    </div>
    ${(doc.status === 'loading' || lists) && html`<div class="flex flex-col gap-2">${ROLES.map((r) => html`<div key=${r.id} class="skeleton h-[92px] rounded-xl"></div>`)}</div>`}
    ${doc.status === 'error' && html`<${ErrorBlock} title="Could not load settings" message=${doc.error} onRetry=${load} />`}
    ${doc.status === 'ready' && !lists && html`<div class="flex flex-col gap-3">
      ${ROLES.filter((r) => !r.group).map((role) => html`<${RoleCard} key=${role.id} role=${role} value=${doc.d.models?.[role.id]}
        onSaved=${(d) => setDoc({ status: 'ready', d })} />`)}
      <div class="mt-3 flex items-center gap-2">
        <h3 class="font-medium">${MEDIA.title}</h3>
        <span class="rounded px-1 border border-line text-[10px] leading-4 text-faint">Later</span>
      </div>
      <p class="text-muted text-[13px] -mt-2">${MEDIA.desc}</p>
      ${ROLES.filter((r) => r.group === 'media').map((role) => html`<${RoleCard} key=${role.id} role=${role} value=${doc.d.models?.[role.id]}
        onSaved=${(d) => setDoc({ status: 'ready', d })} />`)}
    </div>`}
  </div>`;
}

function RoleCard({ role, value, onSaved }) {
  const s = useStore();
  const [busy, setBusy] = useState(false);
  const cur = encode(value);
  const agentId = value?.agent || '';

  // The models of the role's kinds. Only chat keeps the composer's
  // context-window floor (listed): a title or a vector needs no long
  // context, and the small models that suit them fall under it.
  const text = role.kinds.includes('text');
  const fits = (m) => role.kinds.includes(m.modelType) || (text && !m.modelType);
  const shown = (m) => !role.agents || m.listed !== false;
  const usable = s.providers.items.filter((p) => !p.broken);
  const groups = usable.map((p) => ({
    label: `${p.name || p.id} · API`,
    options: p.models.filter((m) => shown(m) && fits(m)).map((m) => ({ value: `p:${p.id}\n${m.id}`, text: m.name || m.id })),
  })).filter((g) => g.options.length);
  const agents = role.agents && s.agents.items.length
    ? [{ label: 'Agents · ACP', options: s.agents.items.map((a) => ({ value: 'a:' + a.id, text: a.name || a.id })) }] : [];
  let options = [{ value: '', text: role.none }, ...agents, ...groups];
  // A role naming something since removed still shows what it names.
  const known = options.some((o) => o.value === cur || o.options?.some((x) => x.value === cur));
  if (cur && !known) options = [...options, { value: cur, text: `${value.agent || `${value.provider} · ${value.model}`} (not found)` }];

  // name is what the toast calls the choice: the option's text, not its id.
  const save = async (next, name) => {
    setBusy(true);
    const vals = { [`models.${role.id}.provider`]: next.provider, [`models.${role.id}.model`]: next.model };
    if (role.agents) vals[`models.${role.id}.agent`] = next.agent;
    try {
      const d = await api.setSettings(vals);
      state.threadDefault = d.models?.threads || null;
      emit();
      onSaved(d);
      toast(`${role.title}: ${name}.`);
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
      ${agentId && html`<${Select} label=${role.title + ' agent model'} class=${cx('sm:w-56', am?.status === 'loading' && 'opacity-60')}
        value=${value.model || ''}
        options=${[{ value: '', text: am?.status === 'loading' ? 'Loading models…' : 'Agent’s default model' },
          ...(am?.list || []).map((m) => ({ value: m.id, text: m.name || m.id }))]}
        onChange=${(m) => save({ agent: agentId, provider: '', model: m }, (am?.list || []).find((x) => x.id === m)?.name || m || 'Agent’s default model')} />`}
    </div>
    ${am?.status === 'error' && html`<p class="text-xs text-bad sm:pl-11">The agent did not list its models: ${am.error}</p>`}
    ${!groups.length && !agents.length && (text
      ? html`<p class="text-xs text-faint sm:pl-11">Add a provider${role.agents ? ' or an agent' : ''} to choose one.</p>`
      : html`<p class="text-xs text-faint sm:pl-11">No model is marked as ${role.kinds[0]}. A provider’s catalog marks them when it can; otherwise set a model’s type under <a href="#/settings/providers" class="underline">Providers</a>.</p>`)}
  </section>`;
}
