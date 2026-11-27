// Skills: the catalog, and who may change each skill. A level move records
// the owner's decision against the digest the page showed; it does not
// rewrite the file. The queue above the list dismisses a waiting proposal.
// Accepting one is not on this page: the wire has no body to show first.
import { html, useEffect, useRef, useState } from '/vendor/preact/htm-preact.mjs';
import { useStore, toast } from '../lib/store.js';
import { api, errorText } from '../lib/api.js';
import { Icon } from './icons.js';
import { Button, Dialog, ErrorBlock, Spinner } from './ui.js';
import {
  DISMISS_CONFIRM_UNKNOWN, DISMISS_NOT_YET, LEFTOVER_ROW, QUEUE_EMPTY, QUEUE_LOAD_FAILED, QUEUE_PAUSED, QUEUE_STILL_READING,
  SKILL_GONE, UNTRACKED_REASON, dismissDraft, dismissFailure, dismissReopenId, dismissSheetCopy, dismissedNotice, dismissWait, noteSkillsRoute,
  proposalCount, proposalFileOnDisk, proposalNoLongerApplies, queuePaused, queueReadPhase, setDismissDraft, skillLabel, skillMovable,
  skillMoveFailure, skillMoveUnknown, skillRouteMissing, sourceLabel, workspaceChoices,
} from '../lib/skills.js';

const inputCls = 'h-8 rounded-lg bg-raised border border-line px-2.5 text-[13px] outline-none focus:border-accent w-full';

export function Skills() {
  const s = useStore();
  const [workspace, setWorkspace] = useState('');
  const [list, setList] = useState({ status: 'loading', items: [], diagnostics: [], pending: 0, pendingStale: 0, error: '' });
  const [session, setSession] = useState(null);
  const [fault, setFault] = useState('');
  // The move's outcome is unknown (no answer): the fault says so, and the
  // owner's way to see the server's state is a reload of the list.
  const [unknown, setUnknown] = useState(false);
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);
  const had = useRef(false);

  // load resolves true only when a read answered with the list.
  const load = async (quiet) => {
    const mine = ++seq.current;
    const ws = workspace;
    if (!quiet) setList((l) => ({ ...l, status: l.status === 'ready' || l.status === 'refreshing' ? 'refreshing' : 'loading' }));
    try {
      const r = await api.skills(ws);
      if (mine !== seq.current) return false;
      noteSkillsRoute();
      if (!Array.isArray(r?.data)) {
        setList((l) => ({ ...l, status: 'error', error: 'The skills list did not come back as a list.' }));
        return false;
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
      return true;
    } catch (e) {
      if (mine !== seq.current) return false;
      if (skillRouteMissing(e)) {
        noteSkillsRoute(e);
        setList((l) => ({ ...l, status: 'absent', error: '' }));
        return false;
      }
      noteSkillsRoute(e);
      const message = errorText(e);
      if (quiet && had.current) {
        setList((l) => ({ ...l, status: 'ready' }));
        toast(message, 'error');
        return false;
      }
      setList((l) => ({ ...l, status: 'error', error: message }));
      return false;
    }
  };
  useEffect(() => { load(); }, [workspace]);

  const pickWorkspace = (value) => {
    setSession(null);
    setFault('');
    setUnknown(false);
    setWorkspace(value);
  };
  const open = (row) => {
    setFault('');
    setUnknown(false);
    setSession({ name: row.name, digest: row.digest, workspace });
  };
  const save = async (level) => {
    if (!session || busy) return;
    const live = list.items.find((row) => row.name === session.name);
    if (!live || live.digest !== session.digest || level === live.level) return;
    setBusy(true);
    setFault('');
    setUnknown(false);
    try {
      await api.moveSkillLevel(session.name, { workspace: session.workspace, level, digest: session.digest });
      const name = session.name;
      setSession(null);
      toast(level === 'agent' ? `The loop may propose changes to ${name}.` : `Only you can change ${name}.`);
      await load(true);
    } catch (err) {
      const message = skillMoveFailure(err);
      setFault(message);
      if (skillMoveUnknown(err)) {
        // No answer: no error toast, and no automatic reload that would
        // look like a verdict. The owner reloads to see what was saved.
        setUnknown(true);
        return;
      }
      toast(message, 'error');
      await load(true);
    } finally {
      setBusy(false);
    }
  };
  // After an unknown outcome the fault stays until a read answers: a
  // failed reload must not look like the move was settled.
  const reloadAfterUnknown = async () => {
    if (await load()) {
      setFault('');
      setUnknown(false);
    }
  };

  const choices = workspaceChoices(s.projects.items);
  if (workspace && !choices.some((opt) => opt.value === workspace)) choices.push({ value: workspace, text: workspace });
  const live = session ? list.items.find((row) => row.name === session.name) : null;
  const shown = list.status === 'ready' || list.status === 'refreshing';
  if (list.status === 'absent') return null;

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

    <${ProposalQueue} workspace=${workspace} skills=${shown ? list.items : []} pending=${list.pending} pendingStale=${list.pendingStale} onDecided=${() => load(true)} />

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

    ${session && html`<${MoveDialog} key=${session.name} session=${session} live=${live} fault=${fault} unknown=${unknown} busy=${busy}
      onClose=${() => !busy && setSession(null)} onSave=${save} onReload=${reloadAfterUnknown} />`}
  </div>`;
}

function MoveDialog({ session, live, fault, unknown, busy, onClose, onSave, onReload }) {
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
      ${fault && unknown && html`<div class="mt-2"><${Button} kind="ghost" disabled=${busy} onClick=${onReload}>Reload list</${Button}></div>`}
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

// ProposalQueue is the waiting proposals, above the catalog. It loads on its
// own: the catalog does not wait for it, and a slow queue never blocks the list.
function ProposalQueue({ workspace, skills, pending, pendingStale, onDecided }) {
  const [queue, setQueue] = useState({ status: 'loading', items: [] });
  const [elapsed, setElapsed] = useState(0);
  const [sheetId, setSheetId] = useState(null);
  const seq = useRef(0);
  const reopened = useRef(false);
  const summary = proposalCount(pending, pendingStale);
  const paused = queuePaused(pending, pendingStale);

  // A quiet read keeps the rows on screen. The first read for a workspace
  // drops them, so a row from the previous workspace cannot be dismissed.
  const loadQueue = async (quiet) => {
    const mine = ++seq.current;
    const ws = workspace;
    if (!quiet) {
      setQueue({ status: 'loading', items: [] });
      setElapsed(0);
    }
    try {
      const r = await api.skillProposals(ws);
      if (mine !== seq.current) return null;
      if (!Array.isArray(r?.data)) {
        if (!quiet) setQueue({ status: 'error', items: [] });
        return null;
      }
      setQueue({ status: 'ready', items: r.data });
      return r.data;
    } catch (err) {
      if (mine !== seq.current) return null;
      if (skillRouteMissing(err)) {
        noteSkillsRoute(err);
        setQueue({ status: 'absent', items: [] });
        return null;
      }
      // A refresh that fails leaves the rows in place. The caller sees that
      // the read did not answer, and the sheet the owner is in stays open.
      if (quiet) return null;
      setQueue({ status: 'error', items: [] });
      return null;
    }
  };
  useEffect(() => { loadQueue(); }, [workspace]);
  useEffect(() => {
    if (queue.status !== 'loading') return;
    const start = Date.now();
    const timer = setInterval(() => {
      const ms = Date.now() - start;
      setElapsed(ms);
      if (ms >= 30000) {
        seq.current += 1;
        setQueue({ status: 'error', items: [] });
      }
    }, 200);
    return () => clearInterval(timer);
  }, [queue.status, workspace]);
  // A session that lapsed left one reason. Open that sheet once the row is
  // back; the owner presses Dismiss, the page does not.
  useEffect(() => {
    if (reopened.current || queue.status !== 'ready') return;
    const id = dismissReopenId();
    if (!id || !queue.items.some((row) => row.id === id)) return;
    reopened.current = true;
    setSheetId(id);
  }, [queue.status, queue.items]);

  if (queue.status === 'absent') return null;
  const phase = queue.status === 'loading' ? queueReadPhase(elapsed) : '';
  const sheet = sheetId ? queue.items.find((row) => row.id === sheetId) : null;
  const retry = () => { seq.current += 1; setQueue({ status: 'loading', items: [] }); setElapsed(0); loadQueue(); };
  return html`<section aria-label="Proposals" class="mb-6">
    ${paused && html`<p class="mb-3 text-[13px]">${QUEUE_PAUSED}</p>`}
    ${summary && html`<p class="mb-3 text-[13px] text-muted">${summary}</p>`}
    ${queue.status === 'error' && html`<${ErrorBlock} title=${QUEUE_LOAD_FAILED} onRetry=${retry} />`}
    ${phase === 'skeleton' && html`<div aria-busy="true" class="flex flex-col gap-2">${[0, 1].map((i) => html`<div key=${i} class="skeleton h-[68px] rounded-xl"></div>`)}</div>`}
    ${(phase === 'noted' || phase === 'retry') && html`<div aria-busy="true">
      <p class="mb-2 text-[13px] text-muted">${QUEUE_STILL_READING}</p>
      <div class="flex flex-col gap-2">${[0, 1].map((i) => html`<div key=${i} class="skeleton h-[68px] rounded-xl"></div>`)}</div>
      ${phase === 'retry' && html`<div class="mt-3"><${Button} onClick=${retry}>Retry</${Button}></div>`}
    </div>`}
    ${queue.status === 'ready' && queue.items.length === 0 && !summary && html`<p class="text-[13px] text-muted">${QUEUE_EMPTY}</p>`}
    ${queue.status === 'ready' && queue.items.length > 0 && html`<ul class="rounded-xl border border-line bg-panel divide-y divide-line">
      ${queue.items.map((row) => html`<li key=${row.id} class="flex flex-col gap-3 sm:flex-row sm:items-start px-4 py-3.5">
        <div class="min-w-0 flex-1 flex flex-col gap-1">
          <span class="text-[13px] font-medium break-words">${row.name}</span>
          ${row.description && html`<p class="text-[13px] text-muted break-words">${row.description}</p>`}
          ${row.reason && html`<p class="text-[13px] text-muted break-words">${row.reason}</p>`}
          ${row.evidence?.quote && html`<p class="text-[13px] text-muted break-words">${row.evidence.quote}</p>`}
          ${proposalNoLongerApplies(row) && html`<p class="text-[13px] break-words">${proposalNoLongerApplies(row)}</p>`}
          ${row.open === true && proposalFileOnDisk(row, skills) && html`<p class="text-[13px] break-words">${LEFTOVER_ROW}</p>`}
        </div>
        <${Button} class="w-full sm:w-auto" onClick=${() => setSheetId(row.id)}>Dismiss</${Button}>
      </li>`)}
    </ul>`}
    ${sheet && html`<${DismissDialog} key=${sheet.id} proposal=${sheet} skills=${skills} workspace=${workspace}
      onClose=${() => setSheetId(null)} reload=${() => loadQueue(true)} onDecided=${onDecided} />`}
  </section>`;
}

function DismissDialog({ proposal, skills, workspace, onClose, reload, onDecided }) {
  const [reason, setReason] = useState(dismissDraft(proposal.id));
  const [fault, setFault] = useState('');
  const [busy, setBusy] = useState(false);
  const [waited, setWaited] = useState(0);
  const faultRef = useRef(null);
  const copy = dismissSheetCopy(proposal, skills);
  const wait = dismissWait(busy ? waited : 0);
  const described = [copy.help ? 'dismiss-help' : '', fault ? 'dismiss-fault' : ''].filter(Boolean).join(' ');
  useEffect(() => { if (fault) faultRef.current?.focus(); }, [fault]);
  useEffect(() => {
    if (!busy) { setWaited(0); return; }
    const start = Date.now();
    const timer = setInterval(() => setWaited(Date.now() - start), 200);
    return () => clearInterval(timer);
  }, [busy]);
  const close = () => { if (!busy) onClose(); };
  const onReason = (value) => {
    setReason(value);
    setDismissDraft(proposal.id, value);
    if (fault) setFault('');
  };
  // recorded closes the sheet and confirms the state the owner sent.
  // A read that still lists the row did not record it.
  const recorded = async (wasOpen) => {
    const items = await reload();
    await onDecided();
    setBusy(false);
    // A read that did not answer cannot prove the row left, so it is not confirmed.
    if (!Array.isArray(items)) return false;
    if (items.some((row) => row.id === proposal.id)) return false;
    toast(dismissedNotice(proposal.name, wasOpen));
    setDismissDraft(proposal.id, '');
    setFault('');
    onClose();
    return true;
  };
  const submit = async (e) => {
    e.preventDefault();
    const text = reason.trim();
    if (!text || busy) return;
    const wasOpen = proposal.open === true;
    setBusy(true);
    setFault('');
    try {
      await api.decideSkillProposal(proposal.id, { workspace, action: 'dismiss', reason: text });
      const gone = await recorded(wasOpen);
      if (!gone) setFault(DISMISS_NOT_YET);
    } catch (err) {
      const outcome = dismissFailure(err);
      if (outcome.kind === 'decided') {
        const gone = await recorded(wasOpen);
        if (!gone) onClose();
        return;
      }
      if (outcome.kind === 'unknown') {
        setFault(DISMISS_CONFIRM_UNKNOWN);
        const gone = await recorded(wasOpen);
        if (!gone) setFault(DISMISS_NOT_YET);
        return;
      }
      if (outcome.kind === 'gone') {
        toast(outcome.message);
        setDismissDraft(proposal.id, '');
        setBusy(false);
        await reload();
        await onDecided();
        onClose();
        return;
      }
      if (outcome.kind === 'login') {
        setDismissDraft(proposal.id, reason);
        setBusy(false);
        return;
      }
      setFault(outcome.message);
      setBusy(false);
      await reload();
      await onDecided();
    }
  };
  return html`<${Dialog} open=${true} onClose=${close} title=${proposal.name}
    footer=${html`<${Button} kind="ghost" disabled=${busy} onClick=${close}>Cancel</${Button}>
      <${Button} kind="primary" type="submit" form="skill-dismiss" busy=${wait.spinner} disabled=${!reason.trim() || busy}>Dismiss</${Button}>`}>
    <form id="skill-dismiss" method="post" noValidate onSubmit=${submit}>
      ${proposal.description && html`<p class="text-[13px] text-muted break-words">${proposal.description}</p>`}
      ${proposal.reason && html`<p class="mt-2 text-[13px] text-muted break-words">${proposal.reason}</p>`}
      ${proposal.evidence?.quote && html`<p class="mt-2 text-[13px] text-muted break-words">${proposal.evidence.quote}</p>`}
      ${copy.file && html`<p class="mt-3 text-[13px] break-words">${copy.file}</p>`}
      ${copy.patch && html`<p class="mt-3 text-[13px] break-words">${copy.patch}</p>`}
      ${copy.help && html`<p id="dismiss-help" class="mt-3 text-[13px] text-muted break-words">${copy.help}</p>`}
      <div class="mt-4 flex flex-col gap-1">
        <label for="dismiss-reason" class="text-[13px] font-medium">Reason</label>
        <textarea id="dismiss-reason" name="reason" required autofocus rows="3" disabled=${busy} value=${reason}
          aria-describedby=${described || undefined} aria-invalid=${fault ? 'true' : 'false'}
          onInput=${(e) => onReason(e.target.value)}
          class="w-full rounded-lg bg-raised border border-line px-2.5 py-2 text-[13px] outline-none focus:border-accent"></textarea>
      </div>
      ${fault && html`<p ref=${faultRef} id="dismiss-fault" tabIndex=${-1} role="alert" class="mt-3 text-[13px]">${fault}</p>`}
      ${wait.note && html`<p role="status" class="mt-3 text-[13px] text-muted">${wait.note}</p>`}
    </form>
  </${Dialog}>`;
}
