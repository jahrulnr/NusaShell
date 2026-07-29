import { describe, expect, it } from "vitest";
import { inspectAttachmentContent } from "../src/renderer/attachment-content.js";

describe("attachment content inspection", () => {
  it("accepts UTF-8 source text without relying on a filename or declared MIME type", () => {
    expect(inspectAttachmentContent(new TextEncoder().encode("export const answer = 42;"))).toEqual({
      kind: "text",
      mediaType: "text/plain",
      content: "export const answer = 42;",
    });
  });

  it("recognizes PDF and PNG from their data signatures", () => {
    expect(inspectAttachmentContent(new TextEncoder().encode("%PDF-1.7\n"))).toMatchObject({
      kind: "file",
      mediaType: "application/pdf",
    });
    expect(inspectAttachmentContent(new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10]))).toMatchObject({
      kind: "image",
      mediaType: "image/png",
    });
  });

  it("rejects binary data that has no supported signature", () => {
    expect(inspectAttachmentContent(new Uint8Array([0, 159, 255, 1, 2, 3]))).toBeNull();
  });
});
