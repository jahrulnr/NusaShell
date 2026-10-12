# Image attachments and vision

Users can attach images (PNG, JPEG, GIF, WebP) to a turn. The runtime
checks whether the active model supports images before sending.

- **Vision-capable model:** images are sent directly to the model.
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
  generate_media with media_type="image".]` This keeps a mid-conversation
  switch to a text-only model from breaking the turn and gives the model
  an actionable way to access the image.

## Vision fallback

When the active chat model does not support vision but the user has
configured a **Vision fallback model** in settings, NusaShell describes
each attached image using the fallback vision model before the first turn
round. The description is injected as a text attachment named
`vision:<filename>` on the user message. The original
image is preserved so a later switch to a vision-capable model can still
see it. Later turns skip any image that already has that text
attachment, so descriptions are never duplicated. Audio
(`audio:<filename>`) and video (`video:<filename>`) follow the same
skip.

If no fallback is configured, non-vision models receive the text placeholder
described above.

## read_media tool

The `read_media` tool loads any media file (image, audio, video, or PDF
document) from disk on demand. It accepts a `file_path` — an absolute
path to a media file on disk — and an optional `question`; relative
paths are rejected. There is no conversation-history lookup: the file
either exists at that absolute path or the tool returns a clear
not-found error.

The media kind (image, audio, video, or document) is auto-detected from
the file's actual bytes — no need to specify it. Extensions can be lied
about (e.g. a `.js` file renamed to `.png`); the content cannot. If the
file does not match any known media type, the tool rejects it with a
clear error.

`read_media` rejects SVG with a clear error because providers do not
accept SVG as image input. Use `show(op=image)` to display SVGs in the
UI instead.

Use a real absolute path — never guess or invent one.

Good example:

    read_media(file_path="/home/user/.config/nusashell/attachments/conv_1/cat.png",
               question="What color is the cat?")

Bad examples:

    read_media(file_path="cat.png")  # relative path is rejected

    read_media(file_path="/home/user/Pictures/guess.png")  # not the attached file

- **Vision-capable model:** the tool output is the absolute file path
  (text); the image itself reaches the model on the next round.
- **Non-vision model + fallback configured:** the image is described using
  the vision fallback model and the text description is returned as the
  tool result.
- **Non-vision model + no fallback:** returns an error message explaining
  that the model cannot see images and no fallback is configured.

The image attachment is preserved on the original user message, so
`read_media` can re-load it even after compaction prunes it.

Audio and video files follow the same on-demand pattern —
`read_media` auto-detects the kind and routes accordingly. PDFs load as
document attachments; non-document-capable models get a placeholder note
with the file path. Note: **PDF support is separate from vision** —
many vision models (Llama, Qwen, Grok, Mistral) cannot read PDFs; only
document-capable models receive them natively.

For a user-facing preview without sending the document to the model, use
`show(op="pdf", path="/absolute/path/report.pdf")`. Office files (DOCX,
PPTX, XLSX, ODT) have no preview path; do not invent a `show` operation
for them.

### Capability gating (hiding media from unsupported models)

Unsupported media attachments are stripped before they reach the
provider. Audio and video go to capable models only — the same handling
applies to image, audio, video, and document (PDF):

1. **User-authored attachments:** the attachment is removed and a
   placeholder is injected telling the model to call `read_media` with
   the absolute file path if it wants the content.
2. **Tool result attachments:** the attachment is removed and a text
   note is appended (e.g. `[Video "clip.mp4" was loaded but cannot be
   shown to this model. File path: /path/to/clip.mp4]` or `[Document
   "report.pdf" was loaded but cannot be read by this model. File path:
   /path/to/report.pdf]`).
3. **Proactive fallback:** when a fallback model is configured, the
   media is described via the fallback before the turn starts, so the
   text-only model receives the content as a text attachment. Images,
   audio, and video that already have a matching `vision:` / `audio:` /
   `video:` text attachment are skipped on retry.

Unknown models default to vision on and audio, video, and document off —
vision is common enough to default on, but the rarer capabilities cause
provider errors when sent to models that lack them.

## Generated images

`generate_media` with `media_type="image"` writes PNG, JPEG, or WebP
files into the conversation's attachments directory.

The tool output tells the model the print is already on screen. Do not
re-render it as Markdown or as an image link.

Good example:

    generate_media(media_type="image", prompt="harbor at night, wet cobblestones, sea-glass reflections")

Bad examples:

    generate_media(media_type="image", prompt="harbor at night")
    # followed by an ![result](...) image link in the assistant reply

To edit a previous print, pass its absolute `file_path` in
`referenced_image_paths`. Paths must be absolute and point to a PNG, JPEG,
or WebP file (max 5); relative paths are rejected.

## Generated speech

`generate_media` with `media_type="speech"` writes audio files (mp3,
wav, ogg, m4a) into the conversation's attachments directory; the file
appears in the conversation — do not embed it again in the reply.

## Generated video

`generate_media` with `media_type="video"` writes video files (mp4,
webm, mov, avi) into the conversation's attachments directory; they
appear in the conversation like generated images and speech.

Image-to-video (i2v) is supported via `referenced_image_paths`: the
first image becomes the first frame, and any additional images guide
style/identity. Models that only support text-to-video reject the
request upstream — the error is surfaced verbatim so the agent can
retry without references or switch to an i2v-capable model.

## Folder attachments

Users can drag and drop a folder onto the conversation area. The folder is
attached as a path-only reference — no bytes are stored. The absolute path
is injected into the user message as a text placeholder:

```
[Folder dropped: /home/user/project. Use file tools to list and read its contents.]
```

The agent can then use file tools (`file_list`, `file_read`, `grep`, etc.)
to explore the directory. Folder attachments are only available in the
desktop shell; in pure web mode the browser does not expose filesystem
paths and the drop is rejected with a clear error.
