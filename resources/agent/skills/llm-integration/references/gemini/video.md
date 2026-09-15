# Gemini — Video generation (Veo)

Veo generates 8-second videos (720p / 1080p / 4K) with natively generated
audio. As of the current docs, Veo is **only** exposed through the
`generateContent`-style `:predictLongRunning` surface — **not** the
Interactions API. See the live note in the official docs.

Auth: `x-goog-api-key: $GEMINI_API_KEY`.
Docs: https://ai.google.dev/gemini-api/docs/veo
Verified 2026-09-10 against the page above (last-updated 2026-09-09 UTC).

## Endpoint — `:predictLongRunning`

```text
POST https://generativelanguage.googleapis.com/v1beta/models/{model}:predictLongRunning
```

- Header: `x-goog-api-key: $GEMINI_API_KEY`
- Body shape: `{"instances": [ {…} ], "parameters": {…}}` (NOT the
  `contents`/`parts` shape used by `:generateContent`).

### Submit — text-to-video

```bash
BASE_URL="https://generativelanguage.googleapis.com/v1beta"
operation_name=$(curl -s "${BASE_URL}/models/veo-3.1-generate-preview:predictLongRunning" \
  -H "x-goog-api-key: $GEMINI_API_KEY" \
  -H "Content-Type: application/json" \
  -X POST \
  -d '{
    "instances": [{
      "prompt": "A cinematic shot of a majestic lion in the savannah."
    }]
  }' | jq -r .name)
```

The response is a **long-running operation** object. Capture `.name` — it is
the polling handle.

## Polling the operation

```bash
while true; do
  status_response=$(curl -s -H "x-goog-api-key: $GEMINI_API_KEY" \
    "${BASE_URL}/${operation_name}")
  is_done=$(echo "${status_response}" | jq .done)
  if [ "${is_done}" = "true" ]; then
    break
  fi
  sleep 10
done
```

- Poll `GET {base}/{operation_name}` with the same API key.
- Terminal state is `"done": true`. The official samples sleep 10s between
  polls; documented latency range is **11s min, up to 6 minutes during peak**.
- There is no documented streaming/progress percentage — only `done` flips.

## Successful output shape

```json
{
  "name": "operations/...",
  "done": true,
  "response": {
    "generateVideoResponse": {
      "generatedSamples": [
        { "video": { "uri": "https://...signed-download-uri..." } }
      ]
    }
  }
}
```

- Download URI lives at
  `.response.generateVideoResponse.generatedSamples[0].video.uri`.
- Download with the same `x-goog-api-key` header and `-L` (follow redirects):

```bash
curl -L -o out.mp4 \
  -H "x-goog-api-key: $GEMINI_API_KEY" "${video_uri}"
```

- Generated videos are stored on the server for **2 days** only. Download
  within that window or lose the artifact. Referencing a video for extension
  resets its 2-day timer.

## Models (snapshot 2026-09-10)

| Model | id | Status | Notes |
|---|---|---|---|
| Veo 3.1 | `veo-3.1-generate-preview` | Preview | Text/Image/Video-to-video; reference images; first+last frame; extension |
| Veo 3.1 Fast | `veo-3.1-fast-generate-preview` | Preview | Faster/cheaper variant |
| Veo 3.1 Lite | `veo-3.1-lite-generate-preview` | Preview | No 4K, no extension, no reference images; `allow_adult` only on image paths |
| Veo 3 (deprecated) | `veo-3.0-generate-001` | Stable→deprecated | 8s only; 720p/1080p; image-to-video only |
| Veo 3 Fast (deprecated) | `veo-3.0-fast-generate-001` | Stable→deprecated | — |

Model ids and preview status are volatile. Verify against
https://ai.google.dev/gemini-api/docs/veo#model-versions before shipping.

## Parameters

`instances[]` fields:

| Field | Type | Notes |
|---|---|---|
| `prompt` | string | Text description; supports audio cues (dialogue in quotes, SFX, ambient) |
| `image` | `Image` (inlineData) | Starting frame for image-to-video |
| `lastFrame` | `Image` (inlineData) | Ending frame; requires `image` (interpolation) |
| `referenceImages` | `VideoGenerationReferenceImage[]` | Up to 3, `referenceType: "asset"`; Veo 3.1 only |
| `video` | `Video` (inlineData) | Previous Veo output to extend; Veo 3.1/3.1 Fast only |

`parameters` block:

| Parameter | Values | Notes |
|---|---|---|
| `aspectRatio` | `"16:9"` (default), `"9:16"` | Portrait is Veo 3.1+ |
| `durationSeconds` | `"4"`, `"6"`, `"8"` | Must be `"8"` for 1080p/4K, reference images, or extension |
| `personGeneration` | `"allow_all"`, `"allow_adult"` | EU/UK/CH/MENA restricted to `allow_adult` |
| `resolution` | `"720p"` (default), `"1080p"`, `"4k"` | 4K not on Lite; extension is 720p only |
| `numberOfVideos` | integer | Docs show `1` per request |
| `seed` | integer | Veo 3 only; improves, does not guarantee, determinism |

### Image-to-video (inlineData)

```json
{
  "instances": [{
    "prompt": "Panning wide shot of a calico kitten sleeping in the sunshine",
    "image": {"inlineData": {"mimeType": "image/png", "data": "<BASE64>"}}
  }]
}
```

### Reference images (Veo 3.1 only)

```json
{
  "instances": [{
    "prompt": "...",
    "referenceImages": [
      {"image": {"inlineData": {"mimeType": "image/png", "data": "<BASE64>"}},
       "referenceType": "asset"}
    ]
  }]
}
```

### First + last frame interpolation (Veo 3.1 only)

```json
{
  "instances": [{
    "prompt": "...",
    "image":      {"inlineData": {"mimeType": "image/png", "data": "<FIRST_BASE64>"}},
    "lastFrame":  {"inlineData": {"mimeType": "image/png", "data": "<LAST_BASE64>"}}
  }]
}
```

### Video extension (Veo 3.1 / 3.1 Fast only)

```json
{
  "instances": [{
    "prompt": "Track the butterfly into the garden...",
    "video": {"inlineData": {"mimeType": "video/mp4", "data": "<BASE64>"}}
  }],
  "parameters": {"numberOfVideos": 1, "resolution": "720p"}
}
```

- Input must be a Veo-generated video, ≤141s, 9:16 or 16:9, 720p.
- Output combines original + extension (up to 148s total).
- Voice is not extended if absent from the last ~1s of input.

## Workflow

1. Pick model id; verify it is still current in the live docs.
2. `POST :predictLongRunning` → capture `operation.name`.
3. Poll `GET {base}/{operation_name}` every ~10s until `done: true`.
4. Extract `generatedSamples[0].video.uri`; `curl -L -o out.mp4` with the
   API key header.
5. Persist locally within 2 days; server storage expires.

## Edge cases

- **Not on Interactions API.** Veo is `:predictLongRunning` only as of the
  verified date. Do not assume `POST /v1beta/interactions` works for video.
- **Audio safety blocks.** Veo 3.1 may block a video due to audio safety
  filters; you are not charged for blocked generations.
- **Multi-video prompting** (referencing/reasoning across multiple videos)
  is not supported and may degrade output.
- **Language support.** English is fully supported; other languages are
  unevaluated and may vary.
- **Watermarking.** All Veo videos carry a SynthID watermark; verifiable via
  the SynthID verification platform.
- **Region restrictions.** EU/UK/CH/MENA force `personGeneration: allow_adult`.
- **4K + 1080K duration.** 1080p and 4K require `durationSeconds: "8"`.
- **Seed is not deterministic.** It only *improves* consistency across calls.

## Error handling

- 400 = bad parameters (aspect/resolution/personGeneration mismatch, bad
  inlineData, missing `image` with `lastFrame`).
- 429 = quota — see [errors.md](errors.md).
- Operation may return `done: true` with an `error` block instead of a
  `response` — inspect both before assuming success.

## Related

- Image generation (Nano Banana) for the starting frame: [images.md](images.md)
- Multimodal **input** (video understanding, not generation): [multimodal.md](multimodal.md)
- Music generation (Lyria): [music.md](music.md)
- Errors / quotas / retries: [errors.md](errors.md)
