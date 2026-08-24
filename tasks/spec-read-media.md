# Spec: Consolidate read_image/audio/video → read_media

## Objective

Replace `read_image`, `read_audio`, `read_video` with a single
`read_media` tool. The media kind is auto-detected from binary magic
bytes via `domain.SniffMagic` — no `op` parameter, no extension
guessing. The agent calls `read_media(file_path)` and NusaShell routes
to the correct handler (image/audio/video) based on the file's actual
content.

Success criteria:

1. `read_image`, `read_audio`, `read_video` removed from tool roster.
2. `read_media(file_path, question?)` added — single tool.
3. Media kind auto-detected from first 32 bytes via `SniffMagic`.
4. Routing: kind=image → executeReadImage, audio → executeReadAudio,
   video → executeReadVideo. No handler code changes.
5. Unknown magic → clear error ("unrecognized media type").
6. All existing read_image/audio/video tests pass (adapted to read_media).
7. Docs + system prompt updated.
8. All gates pass.

## Design

### Tool schema

```
read_media(file_path: string (required), question?: string)
```

### Dispatch (agent_round.go)

```go
case "read_media":
    kind, err := sniffMediaKind(toolCall.Args)  // read path, read 32 bytes, SniffMagic
    if err != nil { ... }
    switch kind {
    case "image": → executeReadImage
    case "audio": → executeReadAudio
    case "video": → executeReadVideo
    default: error "unrecognized media type"
    }
```

### Handler reuse

`executeReadImage`, `executeReadAudio`, `executeReadVideo` stay as-is.
They already call `loadMediaAttachment(kind, path)` which re-validates
magic bytes — kind from sniff matches, so validation passes.

### Backward compat

Old tool names (`read_image`, `read_audio`, `read_video`) are removed
from the roster. Direct calls with old names return "unknown tool".
No alias machinery (consistent with existing dispatcher policy).

## Files touched

```text
infrastructure/tools/toolbox.go    — remove 3 tool defs, add read_media
application/agent_round.go         — replace 3 switch cases with read_media + sniff
application/mediaread.go           — add sniffMediaKind helper
application/mediaread_test.go      — add sniff tests, adapt existing
application/vision_read_image_test.go  — adapt to read_media
application/audio_read_audio_test.go   — adapt to read_media
application/video_read_video_test.go   — adapt to read_media
resources/agent/docs/tools.md      — update roster
resources/agent/docs/agent-attachments.md — update if mentions read_image etc
resources/agent/prompts/system.md  — update if mentions read_image etc
```

## Verification

```text
gofmt
go test ./...
go test -race ./...
go vet ./...
go build ./...
```
