# Image attachments and vision

Users can attach images (PNG, JPEG, GIF, WebP) to a turn. The agent runtime
checks whether the active chat model supports image input using the `Vision`
capability flag from the model catalog (models.dev / OpenRouter).

- **Vision-capable model:** images are sent directly to the model as
  `image_url` (Chat Completions) or `image` (Messages) content blocks.
- **Non-vision model:** images are stripped from the conversation history
  before sending to the provider. A text placeholder is appended to the
  user message that shows the absolute file path(s) of the stripped image(s)
  and instructs the model to call the `read_media` tool with `file_path` to
  load the image.
  Example: `[image content omitted — this model does not support image
  input. Image file(s): /home/user/.config/nusashell/attachments/conv_1/cat.png.
  Call the read_media tool with file_path set to one of the absolute paths
  above to load the image into your context. To edit it or use it as a
  reference, pass its absolute path in referenced_image_paths when calling
  generate_image.]`. This prevents provider
  errors when switching from a vision model to a text-only model
  mid-conversation and gives the model an actionable way to access the
  image. Only absolute paths are accepted — relative paths are rejected to
  avoid ambiguity between the model's working directory and the actual file
  location.

## Vision fallback

When the active chat model does not support vision but the user has
configured a **Vision fallback model** in settings (VisionProviderID +
VisionModelID), the agent describes each attached image using the fallback
vision model before the first turn round. The description is injected as a
text attachment on the user message. The original image is preserved so a
later switch to a vision-capable model can still see it.

If no fallback is configured, non-vision models receive the text placeholder
described above.

## read_media tool

The `read_media` tool lets the model load any media file (image, audio,
video, or PDF document) from disk on demand. It accepts a `file_path`
(any absolute path of a media file on disk — conversation attachments,
generated images, or files elsewhere on the filesystem) and an optional
`question`. Relative paths are rejected — only absolute paths are
accepted. There is no conversation-history lookup: if the file exists
and is readable at that absolute path, it loads; otherwise the tool
returns a clear not-found error.

The media kind (image, audio, video, or document) is auto-detected from
the file's **binary magic number** — no need to specify whether it's an
image, audio, video, or PDF. Extensions can be lied about (e.g. a `.js`
file renamed to `.png`); magic bytes cannot. If the file's leading bytes
do not match any
known media signature, the tool rejects it with a clear error.

SVG images are the one text-based exception: they are detected by scanning
the first 512 bytes for `<svg` (handling `<?xml>` prologs and `<!DOCTYPE>`
declarations). SVG files are supported by `show(op=image)` for UI display
(the frontend renders SVG via `<img src>`, which strips embedded `<script>`
tags — safe for agent-generated SVG).

`read_media` rejects SVG with a clear error because most providers (OpenAI,
Anthropic) do not accept SVG as image input — they reject
`data:image/svg+xml` URLs with a decode error. Use `show(op=image)` to
display SVGs in the UI instead.

Use a real absolute path — never guess or invent one.

Good example:

    read_media(file_path="/home/user/.config/nusashell/attachments/conv_1/cat.png",
               question="What color is the cat?")

Bad examples:

    read_media(file_path="cat.png")  # relative path is rejected

    read_media(file_path="/home/user/Pictures/guess.png")  # not the attached file

- **Vision-capable model (native fast path):** the image is returned
  directly as a tool result attachment. The provider adapter serializes it
  as an `image_url` content block (Chat Completions) or `image` content
  block (Messages) in the tool result, so the model sees the pixels in the
  next round.
- **Non-vision model + fallback configured:** the image is described using
  the vision fallback model and the text description is returned as the
  tool result.
- **Non-vision model + no fallback:** returns an error message explaining
  that the model cannot see images and no fallback is configured.

The image attachment is preserved on the original user message, so
`read_media` can re-load it even after compaction prunes it from the
visible context window.

Audio and video files follow the same on-demand pattern (native
attachment for a capable model, fallback-model description otherwise) —
`read_media` auto-detects the kind and routes accordingly.

PDF documents follow the same pattern: `read_media` auto-detects the PDF
magic bytes (`%PDF-`) and loads the file as a document attachment. The
provider adapter sends it via the native document content part
(`document` block for Anthropic, `input_file` for OpenAI Responses).
Non-document-capable models get a placeholder note with the file path.
Note: **PDF support is separate from vision** — many vision models
(Llama, Qwen, Grok, Mistral) cannot read PDFs. Only models with the
`Document` capability flag (Anthropic Claude, OpenAI GPT-4o+, Google
xAI Grok) receive PDF attachments natively.

### Wire format for media attachments

Each provider adapter serializes media attachments using the content type
the upstream API expects for that modality:

| Modality | Chat Completions | Responses API | Messages (Anthropic) |
|---|---|---|---|
| Image | `image_url` | `input_image` | `image` source |
| Audio | `input_audio` (base64 + format) | `input_audio` (base64 + format) | `image` source (no native audio) |
| Video | `video_url` | `video_url` | `image` source (no native video) |
| Document (PDF) | text placeholder (no native part) | `input_file` (base64) | `document` source (base64) |

Audio and video MUST NOT be sent as `image_url`/`input_image`. Providers
like Nvidia NIM and Stealth reject `data:audio/...` or `data:video/...`
URLs in the image slot with HTTP 400 ("Failed to load image" or generic
"Provider returned error") because they attempt image decoding on a
non-image payload. OpenRouter documents `video_url` as the dedicated
content type for video input on both Chat Completions and Responses.

**OpenAI does not support video input natively.** The OpenAI FAQ states
"No it can not handle videos. It currently supports processing static
images only." The only OpenAI-sanctioned video workaround is extracting
frames and sending them as `input_image` items. `video_url` is an
OpenRouter-specific extension that routes video to providers which do
support it (e.g. Stealth/ox-alpha). For direct OpenAI API calls,
video attachments are hidden from non-video models by capability gating
(see below) — a model with `Video=false` never receives a `video_url`
block.

### Capability gating (hiding media from unsupported models)

Media attachments are stripped before they reach the provider adapter
when the active chat model does not support the corresponding modality.
This is the same handling for image, audio, video, and document (PDF):

1. **User-authored attachments** (`chatMessages`): the attachment is
   removed and a placeholder is injected telling the model to call
   `read_media` with the absolute file
   path if it wants the content.
2. **Tool result attachments** (`filterToolAttachmentsByCaps`): the
   attachment is removed and a text note is appended (e.g.
   `[Video "clip.mp4" was loaded but cannot be shown to this model.
   File path: /path/to/clip.mp4]` or `[Document "report.pdf" was loaded
   but cannot be read by this model. File path: /path/to/report.pdf]`).
3. **Proactive fallback** (`enrichWithAudioDescriptions` /
   `enrichWithVideoDescriptions`): when a fallback model is configured,
   the media is described via the fallback before the turn starts, so
   the text-only model receives the content as a text attachment.

A model's capabilities are resolved from the provider catalog
(`domain.ModelCapabilitiesOf`). Unknown models default to
`Vision=true, Audio=false, Video=false, Document=false` — vision is
common enough to default on, but audio, video, and document are rare
capabilities that cause
provider errors when sent to models that lack them.

## Generated images

`generate_image` writes files under `attachments/<conversationID>/gen-<toolCallID>.<ext>`
(PNG, JPEG, or WebP). Conversation JSON stores `file_path` only — not a
base64 DataURL — so a 2K print does not bloat the transcript. The UI loads
the file through `/local-file?path=`. The next provider round hydrates
bytes from disk so a vision chat model can see the result. Backends are
OpenAI Images and OpenRouter `POST /images`
`/images/generations` (edits are JSON `images[].image_url`, not
multipart).

The tool output tells the model the print is already on screen. Do not
re-render it as Markdown.

Good example:

    generate_image(prompt="harbor at night, wet cobblestones, sea-glass reflections")

Bad examples:

    generate_image(prompt="harbor at night")
    # followed by `![result](/local-file?path=...)` in the assistant reply

To edit a previous print, pass its absolute `file_path` in
`referenced_image_paths`. Paths that are not in this conversation's
attachments or earlier `generate_image` results are rejected.

## Generated speech

`generate_speech` writes files under
`attachments/<conversationID>/speech_<timestamp>.<ext>` (mp3, wav, ogg, m4a).
The attachment carries both a `file_path` (served through `/local-file`)
and an inline `data_url` (base64) so the UI can play the audio before
the server has flushed the file to disk. The frontend renders the
attachment as a `<audio controls preload="metadata">` element inside a
figure with class `agent-message-audio`; the inline `data_url` is
preferred so playback works without an extra HTTP round trip, and
`/local-file?path=<encoded absolute path>` is the fallback when the
inline bytes are absent (e.g. after a snapshot reload).

In addition to the bubble-path attachment renderer, the agent thread
also renders a `generate_speech` tool call as a dedicated
`agent-genaudio-card` (paralel with `agent-genimage-card`) so the
tool call has the same affordances as generate_image and generate_video
(provider/model/voice chips, prompt preview, Download link).

## Generated video

`generate_video` writes files under
`attachments/<conversationID>/clip_<timestamp>.<ext>` (mp4, webm, mov,
avi). Like the other media generators, the attachment carries both a
`file_path` and an inline `data_url` so the UI can play the video
before the server has flushed the file to disk. The frontend renders
the attachment as a `<video controls preload="metadata">` element
inside a figure with class `agent-message-video`; the inline `data_url`
is preferred so playback works without an extra HTTP round trip, and
`/local-file?path=<encoded absolute path>` is the fallback when the
inline bytes are absent (e.g. after a snapshot reload).

Image-to-video (i2v) is supported via `referenced_image_paths`: the
first image becomes the first frame (sent as `frame_images` with
`frame_type=first_frame`), and any additional images are sent as
`input_references` for style/identity guidance. Models that only
support text-to-video will reject the request upstream — the error is
surfaced verbatim so the agent can retry without references or switch
to an i2v-capable model. The Settings → Video generation model picker
shows an `i2i` badge for models with `vision=true` (image input
modality) so the user can check before calling.

In addition to the bubble-path attachment renderer, the agent thread
also renders a `generate_video` tool call as a dedicated
`agent-genvideo-card` (paralel with `agent-genimage-card`) so the tool
call has the same affordances (provider/model/duration/resolution
chips, prompt preview, Download link).

## Folder attachments

Users can drag and drop a folder onto the conversation area. The folder is
attached as a path-only reference (`type: "folder"`) — no bytes are stored.
The absolute path is injected into the user message as a text placeholder:

```
[Folder dropped: /home/user/project. Use file tools to list and read its contents.]
```

The agent can then use file tools (`list_dir`, `read_file`, `grep`, etc.)
to explore the directory. Folder attachments are only available in desktop
shells (Electron) where the browser exposes `File.path`; in pure web mode
the browser does not expose filesystem paths and the drop is rejected with
a clear error.
