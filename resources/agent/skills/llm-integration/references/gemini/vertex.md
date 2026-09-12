# Gemini — Vertex AI

Same Gemini models, different **auth, host, and model id** conventions.
Use when the workload already runs on GCP with ADC / service accounts.

## vs AI Studio

| | AI Studio (`google` / Hermes `gemini`) | Vertex (`google-vertex` / Hermes `vertex`) |
|---|---|---|
| Auth | `GEMINI_API_KEY` / `GOOGLE_API_KEY` + `x-goog-api-key` | ADC / SA JSON / `GOOGLE_CLOUD_API_KEY`; Bearer access token |
| Host | `generativelanguage.googleapis.com` | `aiplatform.googleapis.com` or `{region}-aiplatform.googleapis.com` / multi-region `*.rep.googleapis.com` |
| Chat API | Native `:generateContent` / stream | Native Vertex generateContent **or** OpenAI-compat `…/endpoints/openapi` |
| Model ids | `gemini-3.6-flash` | Often `google/gemini-3.6-flash` |
| Env | `GEMINI_API_KEY` | `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`, `VERTEX_*` |

## Hermes Vertex

- Provider: `vertex`
- Config: `vertex.project_id`, `vertex.region` (default `global`)
- Creds: `VERTEX_CREDENTIALS_PATH` or `GOOGLE_APPLICATION_CREDENTIALS`
- Runtime base (OpenAI-compat):
  `https://{host}/v1beta1/projects/{project}/locations/{region}/endpoints/openapi`
- Gemini 3.x previews often need region **`global`**
- Refuse sharing another profile’s ADC path (multiplex hazard)
- Mid-session 401 → refresh token and retry once

## OpenClaw Vertex

- Provider: `google-vertex`
- Stream URL pattern:
  `{origin}/v1/projects/{p}/locations/{loc}/publishers/google/models/{id}:streamGenerateContent?alt=sse`
- Multi-region `eu` / `us` → `aiplatform.{eu|us}.rep.googleapis.com` (not
  `{region}-aiplatform`)
- Setup via onboard / plugin manifest cloud credentials

## Thinking on Vertex OpenAI-compat

When using the OpenAI-compat endpoint, thinking often rides as:

```json
"extra_body": {
  "google": {
    "thinking_config": {
      "include_thoughts": true,
      "thinking_level": "low"
    }
  }
}
```

(snake_case under `google.thinking_config`). Native Vertex generateContent
uses the same camelCase `thinkingConfig` as AI Studio ([thinking.md](thinking.md)).

## Workflow

1. Prefer AI Studio keys for local/dev agents unless GCP is mandated.
2. On Vertex: resolve project+region first; use `global` for Gemini 3 previews
   when regional 404s appear.
3. Keep auth refresh on long agent sessions.
4. Do not send AI Studio API keys as Vertex Bearer tokens.

## Error handling

Auth refresh vs permanent 403 — distinguish before retry storms.
[errors.md](errors.md).
