# OpenAI — Video generation (Videos API / Sora)

Async video: **submit → poll (or webhook) → download**.

## Hard product warning (read first)

Sora 2 + the Videos API are **deprecated**. OpenAI's official notice schedules
the API shutdown for **2026-09-24** (`sora-2`, `sora-2-pro`, and listed
snapshots); verify the current status before acting on this date. The notice
does not identify an OpenAI API replacement — do not assume one.

Source: <https://help.openai.com/en/articles/20001152-what-to-know-about-the-sora-discontinuation>

- **New video features** → prefer OpenRouter (`../openrouter/video.md`)
  or another video provider — do not start greenfield OpenAI Videos work.
- **Existing OpenAI Videos integrations** → migrate before shutdown; after the
  date expect hard failures (e.g. 410/404), and stored job assets may be gone.

This file documents the contract for maintenance / migration only.

## Positive case — submit

Multipart (interactive API):

```bash
curl -X POST "https://api.openai.com/v1/videos" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -H "Content-Type: multipart/form-data" \
  -F prompt="Wide tracking shot of a teal coupe on a desert highway, hard sun, heat ripples." \
  -F model="sora-2" \
  -F size="1280x720" \
  -F seconds="8"
```

Immediate job object (not the MP4):

```json
{
  "id": "video_...",
  "object": "video",
  "created_at": 1758941485,
  "status": "queued",
  "model": "sora-2",
  "progress": 0,
  "seconds": "8",
  "size": "1280x720"
}
```

## Request contract — `POST /v1/videos`

| Field | Notes |
|---|---|
| `model` | `sora-2` (faster) or `sora-2-pro` (higher fidelity / 1080p) |
| `prompt` | Shot type, subject, action, setting, lighting |
| `size` | Resolution string (e.g. `1280x720`; 1080p sizes need `sora-2-pro`) |
| `seconds` | Clip length; models support longer clips (up to ~16–20s on current Sora 2 docs) |
| `input_reference` | Optional image guide (multipart file, or JSON `file_id` / `image_url` in Batch) |

Batch path: `POST /v1/videos` via Batch API uses **JSON**, not multipart;
upload assets first. Batch downloads expire (~24h after batch completion).

## Lifecycle contract

1. `POST /v1/videos` → `{id, status}` (`queued` / `in_progress`)
2. Poll `GET /v1/videos/{video_id}` until `completed` or `failed`
   (or subscribe to webhooks)
3. Download `GET /v1/videos/{video_id}/content` → MP4 bytes

Statuses: `queued` → `in_progress` → `completed` | `failed`.

Poll every ~2–20s with backoff. Renders can take **minutes** — do not use a
short absolute request timeout around the whole job; cancel via context /
user abort instead.

## Positive case — poll + download

```bash
# poll
curl "https://api.openai.com/v1/videos/$VIDEO_ID" \
  -H "Authorization: Bearer $OPENAI_API_KEY"

# download when status=completed
curl "https://api.openai.com/v1/videos/$VIDEO_ID/content" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  --output out.mp4
```

## Guardrails (expected failures, not bugs)

API rejects / fails jobs for:

- Content not suitable for under-18 audiences (default)
- Copyrighted characters / music
- Real people / public figures
- Human-likeness character uploads / face reference images (blocked by default)

Treat these as **non-retryable** policy failures; rewrite prompt/assets.

## Chat video input

OpenAI Chat Completions / Responses **do not accept** `video_url` /
`input_video` parts (400). Video understanding ≠ Videos API. Route
understanding to OpenRouter or extract frames client-side — see
`multimodal.md`.

## Workflow (maintenance)

1. Confirm whether the product still needs OpenAI Videos before shutdown.
2. If yes: keep submit/poll/download; surface `failed` errors with job id.
3. If migrating: map prompt/`seconds`/resolution to OpenRouter (or other)
   video models; keep the same UX state machine (queued → ready → file).
4. Cap downloaded bytes; verify MP4 playability before returning to users.

## Edge cases

- **Treating create response as a file** — create returns a job, not MP4 bytes.
- **Polling forever** — stop on `failed`, caller cancel, or product-level max
  wait; do not invent a silent success.
- **Batch vs multipart** — Batch JSON cannot upload multipart
  `input_reference` video blobs the same way; pre-upload files.
- **1080p + long duration** → much higher latency/cost; use for final export,
  not prompt iteration (`sora-2` + shorter clips for exploration).
- After shutdown date: do not add retries hoping the API returns — migrate.

## Error handling

400 = bad params / policy text on create; job `failed` = check error on the
job object (do not re-download content); 429/5xx on poll = backoff. See
`errors.md` for HTTP envelope. Policy rejects are not transient.
