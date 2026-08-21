# Image attachments and vision

Users can attach images (PNG, JPEG, GIF, WebP) to a turn. The agent runtime
checks whether the active chat model supports image input using the `Vision`
capability flag from the model catalog (models.dev / OpenRouter).

- **Vision-capable model:** images are sent directly to the model as
  `image_url` (Chat Completions) or `image` (Messages) content blocks.
- **Non-vision model:** images are stripped from the conversation history
  before sending to the provider. A text placeholder is appended to the
  user message that shows the absolute file path(s) of the stripped image(s)
  and instructs the model to call the `read_image` tool with `file_path` to
  load the image.
  Example: `[image content omitted — this model does not support image
  input. Image file(s): /home/user/.config/nusashell/attachments/conv_1/cat.png.
  Call the read_image tool with file_path set to one of the absolute paths
  above to load the image into your context.]`. This prevents provider
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

## read_image tool

The `read_image` tool lets the model request an image from the conversation
on demand. It accepts a `file_path` (the absolute path of an image file on
disk, shown in the image placeholder) and an optional `question`. Relative
paths are rejected — only absolute paths are accepted.

Use the absolute path from the placeholder — never guess or invent one.

Good example:

    read_image(file_path="/home/user/.config/nusashell/attachments/conv_1/cat.png",
               question="What color is the cat?")

Bad examples:

    read_image(file_path="cat.png")  # relative path is rejected

    read_image(file_path="/home/user/Pictures/guess.png")  # not the attached file

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
`read_image` can re-load it even after compaction prunes it from the
visible context window.

`read_audio` and `read_video` follow the same on-demand pattern (native
attachment for a capable model, fallback-model description otherwise) for
audio and video files respectively.

## Generated images

`generate_image` writes files under `attachments/<conversationID>/gen-<toolCallID>.<ext>`
(PNG, JPEG, or WebP). Conversation JSON stores `file_path` only — not a
base64 DataURL — so a 2K print does not bloat the transcript. The UI loads
the file through `/local-file?path=`. The next provider round hydrates
bytes from disk so a vision chat model can see the result. Backends are
OpenAI Images, OpenRouter `POST /images`, and Codex ChatGPT plan
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
