# OpenRouter — Sign In with OAuth (PKCE)

Add “Sign in with OpenRouter” to a browser app. Users authorize on OpenRouter
and your app receives an API key — **no client registration, no backend secret**.

Live demo: [openrouterteam.github.io/sign-in-with-openrouter](https://openrouterteam.github.io/sign-in-with-openrouter/)

Upstream: [openrouter-oauth](https://github.com/OpenRouterTeam/skills/tree/main/skills/openrouter-oauth).
Docs: [OAuth PKCE](https://openrouter.ai/docs/guides/overview/auth/oauth),
[Authentication](https://openrouter.ai/docs/api/reference/authentication).

## Decision tree

| User wants… | Do this |
|---|---|
| Sign-in / login in a web app | Full PKCE flow (+ optional button UX) |
| API key programmatically (no UI chrome) | PKCE flow only |
| Call chat/completions after auth | PKCE here → use key with [chat-completion.md](chat-completion.md) |

Requires browser Web Crypto + `sessionStorage` / `localStorage`.

## OAuth PKCE flow

No client ID or secret — the PKCE challenge is the identity proof.

### 1. Generate verifier and challenge

```
code_verifier  = base64url(32 random bytes)
code_challenge = base64url(SHA-256(code_verifier))
```

- Random: CSPRNG 32 bytes (e.g. `crypto.getRandomValues`).
- base64url: standard base64, then `+`→`-`, `/`→`_`, strip `=`.
- Store `code_verifier` in **`sessionStorage`** (not `localStorage`) so it
  does not persist after the tab closes or leak across tabs.

### 2. Redirect to OpenRouter

```
https://openrouter.ai/auth?callback_url={url}&code_challenge={challenge}&code_challenge_method=S256
```

| Param | Value |
|---|---|
| `callback_url` | App URL where the user returns after auth |
| `code_challenge` | S256 challenge from step 1 |
| `code_challenge_method` | Always `S256` |

### 3. Handle redirect back

User returns to `callback_url` with `?code=`. Extract `code`.

**Guard:** only process `?code=` if a `code_verifier` exists in
`sessionStorage` for *this* tab’s flow. Other routes may use `?code=` for
unrelated reasons.

### 4. Exchange code for API key

```bash
curl -sS -X POST https://openrouter.ai/api/v1/auth/keys \
  -H 'Content-Type: application/json' \
  -d '{
    "code": "<code from query>",
    "code_verifier": "<verifier from sessionStorage>",
    "code_challenge_method": "S256"
  }'
# → { "key": "sk-or-..." }
```

Remove the verifier from `sessionStorage` before or after exchange.

### 5. Store key and clean up

- Persist `key` in `localStorage` (or your secure client store).
- Strip `?code=` from the URL (`history.replaceState`).
- **Cross-tab sync:** listen for `storage` events on the key entry so other
  tabs update on sign-in / sign-out.

## Auth module contract (language-agnostic)

Implement the same surface regardless of framework:

| Operation | Behavior |
|---|---|
| `getApiKey()` | Read stored key or null |
| `setApiKey(key)` / `clearApiKey()` | Write/clear + notify listeners |
| `hasOAuthCallbackPending()` | True iff verifier present in session |
| `initiateOAuth(callbackUrl?)` | Create verifier → challenge → redirect to `/auth` |
| `handleOAuthCallback(code)` | Exchange → store key; throw if verifier missing or non-2xx |

Suggested storage keys: `openrouter_api_key`, `openrouter_code_verifier`.

## Sign-in button (UX guidance)

Optional UI chrome — not required for the HTTP contract.

- Label default: “Sign in with OpenRouter”.
- Logo SVG uses `fill="currentColor"`, or brand fills: light `#7624F4`,
  dark `#C8FF00`.
- Show loading while key exchange runs.
- Variants commonly used: default / minimal / branded / icon / cta; sizes
  sm → xl. Dark mode: invert light/dark backgrounds for branded/cta.

Logo path (reference):

```svg
<svg viewBox="0 0 401.4 293.7" fill="currentColor">
  <path d="M303.9475,17.19926c42.79734,0,77.48933,34.69327,77.48933,77.48933s-34.69199,77.48933-77.48933,77.48933l76.86166,76.86244c9.76367,9.76313,2.84903,26.45667-10.95697,26.45667h-220.88335c-71.32686,0-129.14889-57.82202-129.14889-129.14889S77.64197,17.19926,148.96884,17.19926h154.97866ZM148.96884,68.85881c-42.79607,0-77.48933,34.69327-77.48933,77.48933s34.69327,77.48933,77.48933,77.48933,77.48933-34.69327,77.48933-77.48933-34.69327-77.48933-77.48933-77.48933Z"/>
</svg>
```

## Using the API key

```bash
curl -sS https://openrouter.ai/api/v1/chat/completions \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "openai/gpt-4o-mini",
    "messages": [{"role":"user","content":"Hello!"}]
  }'
```

Responses API shape also works (`/api/v1/responses`) — see OpenRouter docs.
For streaming / tools: [stream.md](stream.md), [tools.md](tools.md),
[chat-completion.md](chat-completion.md).

## Edge cases

- Missing verifier on callback → abort; do not POST exchange.
- Non-2xx on `/auth/keys` → surface status; do not store a partial key.
- Always clear verifier after attempt (success or failure) to avoid reuse.
- `callback_url` must match the page that will read `?code=` in the same tab
  that started OAuth (sessionStorage is tab-scoped).

## Error handling

| Situation | Action |
|---|---|
| Exchange 4xx | Show auth failed; restart PKCE |
| Exchange 5xx / network | Retry once, then restart flow |
| User denies / abandons | No code — leave signed out |
| API calls with key | Same as [errors.md](errors.md) (401/402/429) |

## Related

- [chat-completion.md](chat-completion.md) — use the key after sign-in
- [errors.md](errors.md) — API error / credit handling
- [README.md](README.md) — OpenRouter router
- [OAuth PKCE guide](https://openrouter.ai/docs/guides/overview/auth/oauth)
- [Authentication](https://openrouter.ai/docs/api/reference/authentication)
- [Live demo](https://openrouterteam.github.io/sign-in-with-openrouter/)
