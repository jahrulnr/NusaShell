// Login screen: shown when the server requires a login and this browser
// has no live session. The password goes to /api/auth/login once; the
// session lives in an HttpOnly cookie.
import { html, useState } from '/vendor/preact/htm-preact.mjs';
import { api, errorText } from '../lib/api.js';
import { Logo } from './icons.js';
import { Button, ErrorBlock } from './ui.js';

export function LoginView({ onLoggedIn }) {
  const [pw, setPw] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const submit = async (e) => {
    e.preventDefault();
    if (!pw || busy) return;
    setBusy(true); setErr('');
    try {
      const r = await api.login(pw);
      onLoggedIn(r.csrfToken);
    } catch (e2) {
      setErr(errorText(e2));
      setBusy(false);
    }
  };
  return html`<div class="h-full flex items-center justify-center px-4">
    <form noValidate onSubmit=${submit} class="w-full max-w-sm flex flex-col gap-4">
      <div class="flex items-center gap-2.5 mb-2"><${Logo} size=${28} /><span class="text-[17px] font-semibold">NusaShell</span></div>
      <div>
        <h1 class="text-[15px] font-medium">Log in</h1>
        <p class="text-muted text-[13px] mt-0.5">Enter the owner password for this server.</p>
      </div>
      ${err && html`<${ErrorBlock} title="Could not log in" message=${err} />`}
      <label class="flex flex-col gap-1.5 text-[13px]"><span class="text-muted">Password</span>
        <input type="password" autocomplete="current-password" autofocus value=${pw} onInput=${(e) => setPw(e.target.value)}
          class="h-9 rounded-lg bg-raised border border-line px-3 text-[13px] outline-none focus:border-accent w-full" />
      </label>
      <${Button} kind="primary" type="submit" busy=${busy} disabled=${!pw}>Log in</${Button}>
      <p class="text-faint text-xs">Forgot it? Run <code class="font-mono">nusashell password</code> on the server.</p>
    </form>
  </div>`;
}
