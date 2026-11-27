// Skills: the catalog, and who may change each skill. A level move records
// the owner's decision against the digest the page showed; it does not
// rewrite the file. Proposals are a count only — accepting or dismissing
// one is a different page, and this one has no control that would call it.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { Button, Dialog, ErrorBlock, Spinner } from './ui.js';
import {
  SKILL_GONE, UNTRACKED_REASON, proposalCount, skillLabel, skillMovable, skillMoveFailure, sourceLabel, workspaceChoices,
} from '../lib/skills.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';

export function Skills() {
  const s = useStore();
  const [workspace, setWorkspace] = useState('');
  const [list, setList] = useState({ status: 'loading', items: [], diagnostics: [], pending: 0, pendingStale: 0, error: '' });
  const [session, setSession] = useState(null);
  const [fault, setFault] = useState('');
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);
  const had = useRef(false);

  const load = async (quiet) => {
    const mine = ++seq.current;
    const ws = workspace;
    if (!quiet) setList((l) => ({ ...l, status: l.status === 'ready' || l.status === 'refreshing' ? 'refreshing' : 'loading' }));
    try {
      const r = await api.skills(ws);
      if (mine !== seq.current) return;
      if (!Array.isArray(r?.data)) {
        setList((l) => ({ ...l, status: 'error', error: 'The skills list did not come back as a list.' }));
        return;
      }
      had.current = r.data.length > 0;
      setList({
        status: 'ready',
        items: r.data,
        diagnostics: Array.isArray(r.diagnostics) ? r.diagnostics : [],
        pending: typeof r.pending === 'number' ? r.pending : 0,
        pendingStale: typeof r.pendingStale === 'number' ? r.pendingStale : 0,
        error: '',
      });
    } catch (e) {
      if (mine !== seq.current) return;
      const message = errorText(e);
      if (quiet && had.current) {
        setList((l) => ({ ...l, status: 'ready' }));
        toast(message, 'error');
        return;
      }
      setList((l) => ({ ...l, status: 'error', error: message }));
    }
  };
  useEffect(() => { load(); }, [workspace]);

  const pickWorkspace = (value) => {
    setSession(null);
    setFault('');
    setWorkspace(value);
  };
  const open = (row) => {
    setFault('');
    setSession({ name: row.name, digest: row.digest, workspace });
  };
  const save = async (level) => {
    if (!session || busy) return;
    const live = list.items.find((row) => row.name === session.name);
    if (!live || live.digest !== session.digest || level === live.level) return;
    setBusy(true);
    setFault('');
    try {
      await api.moveSkillLevel(session.name, { workspace: session.workspace, level, digest: session.digest });
      const name = session.name;
      setSession(null);
      toast(level === 'agent' ? `The loop may propose changes to ${name}.` : `Only you can change ${name}.`);
      await load(true);
    } catch (err) {
      const message = skillMoveFailure(err);
      setFault(message);
      toast(message, 'error');
      await load(true);
    } finally {
      setBusy(false);
    }
  };

  const choices = workspaceChoices(s.projects.items);
  if (workspace && !choices.some((opt) => opt.value === workspace)) choices.push({ value: workspace, text: workspace });
  const live = session ? list.items.find((row) => row.name === session.name) : null;
  const summary = proposalCount(list.pending, list.pendingStale);
  const shown = list.status === 'ready' || list.status === 'refreshing';

  return html`<div>
    <div class="mb-4">
      <h2 class="text-[17px] font-semibold">Skills</h2>
      <p class="text-muted text-[13px] mt-0.5">Who may change each skill. Handing a skill to the loop, or taking it back, records your decision and leaves the file as it is.</p>
    </div>

    <search class="mb-4 max-w-sm">
      <label for="skills-workspace" class="block text-xs text-muted mb-1">Workspace</label>
      <select id="skills-workspace" class=${inputCls} value=${workspace} onChange=${(e) => pickWorkspace(e.target.value)}>
        ${choices.map((opt) => html`<option key=${opt.value} value=${opt.value}>${opt.text}</option>`)}
      </select>
      <p class="text-xs text-faint mt-1">A project adds the skills in its folder. The other roots stay in the list.</p>
    </search>

    ${list.diagnostics.length > 0 && html`<div role="status" class="mb-4 rounded-xl border border-line bg-panel px-4 py-3 text-[13px]">
      <p class="font-medium">Some skills could not be read</p>
      <ul class="mt-1 text-muted list-disc ps-4">${list.diagnostics.map((line, i) => html`<li key=${i} class="break-words">${line}</li>`)}</ul>
    </div>`}

    ${summary && html`<p class="mb-3 text-[13px] text-muted">${summary}</p>`}

    ${list.status === 'loading' && html`<div aria-busy="true" class="flex flex-col gap-2">${[0, 1, 2].map((i) => html`<div key=${i} class="skeleton h-[68px] rounded-xl"></div>`)}</div>`}
    ${list.status === 'error' && html`<${ErrorBlock} title="Could not load skills" message=${list.error} onRetry=${() => load()} />`}

    ${shown && html`<div>
      <p class="text-xs text-faint flex items-center gap-1.5 mb-3">
        ${list.status === 'refreshing' && html`<${Spinner} size=${11} />`}
        ${list.items.length} ${list.items.length === 1 ? 'skill' : 'skills'}
      </p>
      ${list.items.length === 0
        ? html`<div class="rounded-xl border border-dashed border-line p-8 text-center">
            <${Icon} name="file" size=${22} class="text-faint mx-auto" />
            <p class="font-medium mt-2">No skills in this list</p>
            <p class="text-muted text-[13px] mt-1">Nothing is installed in the roots this list reads.</p>
          </div>`
        : html`<ul class="rounded-xl border border-line bg-panel divide-y divide-line">
            ${list.items.map((row) => {
              const waiting = proposalCount(row.pending, row.pendingStale);
              return html`<li key=${row.name} class="flex flex-col gap-3 sm:flex-row sm:items-start px-4 py-3.5">
                <div class="min-w-0 flex-1 flex flex-col gap-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="text-[13px] font-medium break-words">${row.name}</span>
                    <span class="inline-flex items-center gap-1 text-xs rounded-md border border-line px-1.5 py-0.5">${skillLabel(row)}</span>
                    <span class="text-xs text-faint">${sourceLabel(row.source)}</span>
                  </div>
                  ${row.description && html`<p class="text-[13px] text-muted break-words">${row.description}</p>`}
                  ${row.path && html`<p class="text-xs text-faint break-all">${row.path}</p>`}
                  ${row.tracked === false && html`<p class="text-xs text-muted">${UNTRACKED_REASON}</p>`}
                  ${waiting && html`<p class="text-xs text-muted">${waiting}</p>`}
                </div>
                ${skillMovable(row) && html`<${Button} class="w-full sm:w-auto" onClick=${() => open(row)}>Change</${Button}>`}
              </li>`;
            })}
          </ul>`}
    </div>`}

    ${session && html`<${MoveDialog} key=${session.name} session=${session} live=${live} fault=${fault} busy=${busy}
      onClose=${() => !busy && setSession(null)} onSave=${save} />`}
  </div>`;
}

function MoveDialog({ session, live, fault, busy, onClose, onSave }) {
  const [choice, setChoice] = useState(live?.level === 'agent' ? 'agent' : 'user');
  const faultRef = useRef(null);
  const stale = !live || live.digest !== session.digest;
  const unchanged = !!live && choice === live.level;
  useEffect(() => { if (fault) faultRef.current?.focus(); }, [fault]);
  const submit = (e) => {
    e.preventDefault();
    if (!stale && !unchanged && !busy) onSave(choice);
  };
  return html`<${Dialog} open=${true} onClose=${onClose} title=${session.name}
    footer=${html`<${Button} kind="ghost" disabled=${busy} onClick=${onClose}>Cancel</${Button}>
      <${Button} kind="primary" type="submit" form="skill-move" busy=${busy} disabled=${stale || unchanged || busy}>Save changes</${Button}>`}>
    <form id="skill-move" method="post" noValidate onSubmit=${submit}>
      ${live?.description && html`<p class="text-[13px] text-muted break-words">${live.description}</p>`}
      ${live?.path && html`<p class="mt-1 text-xs text-faint break-all">${live.path}</p>`}
      ${live?.changed && !stale && html`<p id="skill-hand-back" class="mt-3 text-[13px]">Edited by hand. The loop will not propose a change until you hand this skill back.</p>`}
      ${fault && html`<p ref=${faultRef} tabIndex=${-1} role="alert" class="mt-3 text-[13px]">${fault}</p>`}
      ${!fault && !live && html`<p role="alert" class="mt-3 text-[13px]">${SKILL_GONE}</p>`}
      ${!fault && live && stale && html`<p role="status" class="mt-3 text-[13px]">This file changed after you opened it. Close this and open it again to decide on the current file.</p>`}
      ${live && html`<fieldset class="mt-4 border-0 p-0 m-0" disabled=${stale || busy} aria-describedby=${live.changed && !stale ? 'skill-hand-back' : undefined}>
        <legend class="text-[13px] font-medium mb-1">Who may change it</legend>
        ${[['user', 'Only you'], ['agent', 'Loop may propose']].map(([value, text]) => html`<label key=${value} class="flex items-center gap-2 min-h-12">
          <input type="radio" name="skill-level" value=${value} checked=${choice === value} disabled=${stale || busy}
            onChange=${() => setChoice(value)} />
          <span class="text-[13px]">${text}</span>
        </label>`)}
      </fieldset>`}
    </form>
  </${Dialog}>`;
}
