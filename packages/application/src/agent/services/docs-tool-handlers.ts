import type { DocsIndexPort } from "../ports/docs-index.port.js";
import { clampInt, docsNotConfigured, docsNotReady, requireString, optionalString } from "./gateway-utils.js";

export async function execDocsSearch(index: DocsIndexPort | undefined, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  if (!index) return docsNotConfigured();
  if (!index.usable()) return docsNotReady();
  const query = requireString(args.query, "query");
  const topK = clampInt(args.top_k, 5, 1, 10);
  const hits = await index.search(query, topK);
  return {
    ok: true,
    data: { chunks: hits },
    meta: { count: hits.length, truncated: hits.length >= topK, index_ready: true, data_is_untrusted: true },
  };
}

export async function execDocsList(index: DocsIndexPort | undefined, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  if (!index) return docsNotConfigured();
  if (!index.usable()) return docsNotReady();
  const limit = clampInt(args.limit, 50, 1, 100);
  const documents = await index.listDocs();
  const truncated = documents.length > limit;
  const limited = documents.slice(0, limit);
  return {
    ok: true,
    data: { documents: limited },
    meta: { count: limited.length, truncated, index_ready: true, data_is_untrusted: true },
  };
}

export async function execDocsRead(index: DocsIndexPort | undefined, args: Readonly<Record<string, unknown>>): Promise<unknown> {
  if (!index) return docsNotConfigured();
  if (!index.usable()) return docsNotReady();
  const path = requireString(args.path, "path");
  const chunkId = optionalString(args.chunk_id) || undefined;
  const doc = await index.readDoc(path, chunkId);
  if (!doc) {
    return {
      ok: false,
      error: { code: "not_found", message: "Document not found in docs corpus" },
      meta: { index_ready: true, data_is_untrusted: true },
    };
  }
  const offset = clampInt(args.offset, 0, 0, 1_000_000);
  const maxChars = clampInt(args.max_chars, 0, 0, 20_000);
  const text = maxChars > 0 ? doc.text.slice(offset, offset + maxChars) : doc.text.slice(offset);
  const fullEnd = offset + (maxChars > 0 ? maxChars : doc.text.length);
  const hasMore = fullEnd < doc.text.length;
  return {
    ok: true,
    data: {
      path: doc.path, title: doc.title, headings: doc.headings, domain: doc.domain,
      text, chunk_id: doc.chunkId, has_more: hasMore, next_offset: hasMore ? fullEnd : undefined,
    },
    meta: { index_ready: true, data_is_untrusted: true },
  };
}
