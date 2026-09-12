# ChatGPT backend — file upload

Upload attachments via ChatGPT backend-api **outside** the Codex path.
Base: `https://chatgpt.com/backend-api` (not `/codex`).

Auth (create + finalize only): `Authorization: Bearer <ChatGPT-token>` and
`ChatGPT-Account-ID: <account_id>`. Blob `PUT` uses the signed `upload_url`
(no ChatGPT auth).

Source: [codex-api/files.rs](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/files.rs) (`upload_openai_file`).

## Positive case — create → blob → finalize

1. **Create** — `POST {base}/files`

```json
{
  "file_name": "hello.txt",
  "file_size": 5,
  "use_case": "codex"
}
```

Response: `{ "file_id", "upload_url", "pdf_c2pa_reservation"? }`.

2. **Blob** — `PUT {upload_url}` with the file body stream.

Headers: `Content-Length: <file_size>`, `x-ms-blob-type: BlockBlob`,
`x-ms-client-request-id: <uuid>`. Timeout 60s.

3. **Finalize** — `POST {base}/files/{file_id}/uploaded`

Body normally `{}`. Poll while `status` is `"retry"` (250ms delay, 30s cap).

Success body:

```json
{
  "status": "success",
  "download_url": "https://…",
  "file_name": "hello.txt",
  "mime_type": "text/plain",
  "file_size_bytes": 5
}
```

Canonical URI for Codex/sediment consumers: `sediment://{file_id}`.

## Contract

| Item | Value |
|---|---|
| Base | `https://chatgpt.com/backend-api` |
| Create | `POST /files` |
| Finalize | `POST /files/{file_id}/uploaded` |
| Auth | Bearer ChatGPT-token + `ChatGPT-Account-ID` |
| `use_case` | always `"codex"` (product tag, not URL path) |
| Size limit | 512 MiB (`OPENAI_FILE_UPLOAD_LIMIT_BYTES`) |
| Request timeout | 60s (create / blob / finalize HTTP) |
| Finalize ready | `status`: `success` \| `retry` \| error |

Optional create fields when hosted/connector upload:

| Field | Notes |
|---|---|
| `codex_connector_id` | Connector id |
| `codex_action_name` | Action name |
| `codex_model` | Model id |

If create returns `pdf_c2pa_reservation: true`, finalize body is
`{ "pdf_c2pa_create_request": <original create JSON> }` instead of `{}`.

## Edge cases

- **Path is not under `/codex`.** Do not call
  `…/backend-api/codex/files`. Files live at `{backend-api}/files` and
  `{backend-api}/files/{id}/uploaded` only.
- Oversize before network → client `FileTooLarge` (> 512 MiB).
- Finalize `status: "retry"` past 30s → `UploadNotReady`.
- Other finalize `status` → `UploadFailed` (`error_message` if present).
- Blob failures report Azure/CF diagnostics; do not log SAS query secrets
  from `upload_url`.
- Older servers omit `pdf_c2pa_reservation` → finalize with `{}` even for
  hosted PDF creates.
- Missing `download_url` on success → treat as upload failure.

## Related

- Auth / account headers / ChatGPT session: [chatgpt-backend.md](chatgpt-backend.md)
- Codex responses that attach `sediment://` file URIs: [responses.md](responses.md)
- Router: [README.md](README.md)
