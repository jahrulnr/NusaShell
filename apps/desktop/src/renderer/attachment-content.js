const PNG_SIGNATURE = [137, 80, 78, 71, 13, 10, 26, 10];

/** Detect supported attachments from bytes, never names or supplied MIME types. */
export function inspectAttachmentContent(bytes) {
  const data = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  const binary = binaryAttachment(data);
  if (binary) return binary;
  const content = decodeUtf8Text(data);
  return content === null ? null : { kind: "text", mediaType: "text/plain", content };
}

export function toDataUrl(bytes, mediaType) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `data:${mediaType};base64,${btoa(binary)}`;
}

function binaryAttachment(data) {
  if (startsWith(data, PNG_SIGNATURE)) return { kind: "image", mediaType: "image/png" };
  if (startsWith(data, [255, 216, 255])) return { kind: "image", mediaType: "image/jpeg" };
  if (startsWith(data, [71, 73, 70, 56])) return { kind: "image", mediaType: "image/gif" };
  if (startsWith(data, [37, 80, 68, 70, 45])) return { kind: "file", mediaType: "application/pdf" };
  if (startsWith(data, [82, 73, 70, 70]) && startsWith(data.slice(8), [87, 69, 66, 80])) return { kind: "image", mediaType: "image/webp" };
  return null;
}

function decodeUtf8Text(data) {
  try {
    const text = new TextDecoder("utf-8", { fatal: true }).decode(data);
    return /[\u0000-\u0008\u000E-\u001F]/.test(text) ? null : text;
  } catch {
    return null;
  }
}

function startsWith(data, signature) {
  return data.length >= signature.length && signature.every((byte, index) => data[index] === byte);
}
