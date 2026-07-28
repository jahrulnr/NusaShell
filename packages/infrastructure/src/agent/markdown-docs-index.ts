import { mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, normalize, relative } from "node:path";
import { ApplicationError, type DocContent, type DocsHit, type DocSummary, type DocsIndexPort } from "@nusashell/application";

const INDEX_FILE = "docs_index.json";
const STATUS_FILE = "status.json";
const DEFAULT_TOP_K = 5;
const MAX_EXCERPT = 200;

interface IndexChunk {
  readonly id: string;
  readonly heading: string;
  readonly text: string;
}

interface IndexDocument {
  readonly path: string;
  readonly title: string;
  readonly domain: string;
  readonly headings: readonly string[];
  readonly text: string;
  readonly chunks: readonly IndexChunk[];
}

interface IndexFile {
  readonly documents: readonly IndexDocument[];
}

interface StatusMeta {
  readonly status: "ready" | "building" | "failed" | "missing";
  readonly message?: string;
  readonly at?: number;
  readonly documentCount?: number;
}

export interface MarkdownDocsIndexOptions {
  readonly topK?: number;
}

/**
 * Lexical Markdown docs index. Walks a docs root, chunks documents by second-
 * level headings, persists an index JSON, and serves search/list/read queries.
 */
export class MarkdownDocsIndex implements DocsIndexPort {
  private index: IndexFile | null = null;
  private status: StatusMeta = { status: "missing" };
  private building: Promise<void> | null = null;

  constructor(
    private readonly docsRoot: string,
    private readonly storageRoot: string,
    private readonly options: MarkdownDocsIndexOptions = {},
  ) {}

  usable(): boolean {
    return this.status.status === "ready" && this.index !== null;
  }

  async reindex(): Promise<void> {
    if (this.building) return this.building;
    this.building = this.buildIndex();
    try {
      await this.building;
    } finally {
      this.building = null;
    }
  }

  async search(query: string, topK: number): Promise<readonly DocsHit[]> {
    await this.ensureReady();
    if (!this.usable() || this.index === null) return [];
    const terms = tokenize(query);
    if (terms.length === 0) return [];
    const k = topK > 0 ? topK : (this.options.topK ?? DEFAULT_TOP_K);
    const scored: Array<{ chunk: IndexChunk; doc: IndexDocument; score: number }> = [];
    for (const doc of this.index.documents) {
      for (const chunk of doc.chunks) {
        const score = scoreChunk(chunk, terms);
        if (score > 0) scored.push({ chunk, doc, score });
      }
    }
    scored.sort((a, b) => b.score - a.score);
    return scored.slice(0, k).map(({ chunk, doc, score }) => ({
      path: doc.path,
      title: doc.title,
      heading: chunk.heading,
      chunkId: chunk.id,
      excerpt: makeExcerpt(chunk.text, terms),
      score,
    }));
  }

  async listDocs(): Promise<readonly DocSummary[]> {
    await this.ensureReady();
    if (!this.usable() || this.index === null) return [];
    return this.index.documents.map((doc) => ({
      path: doc.path,
      title: doc.title,
      headings: doc.headings,
      domain: doc.domain,
    }));
  }

  async readDoc(inputPath: string, chunkId?: string): Promise<DocContent | undefined> {
    await this.ensureReady();
    if (!this.usable() || this.index === null) return undefined;
    const path = safeRelativePath(inputPath);
    if (path === undefined) return undefined;
    const doc = this.index.documents.find((d) => d.path === path);
    if (!doc) return undefined;
    if (chunkId) {
      const chunk = doc.chunks.find((c) => c.id === chunkId);
      if (!chunk) return undefined;
      return {
        path: doc.path,
        title: doc.title,
        headings: doc.headings,
        domain: doc.domain,
        text: chunk.text,
        chunkId: chunk.id,
        chunk: chunk.text,
      };
    }
    return {
      path: doc.path,
      title: doc.title,
      headings: doc.headings,
      domain: doc.domain,
      text: doc.text,
    };
  }

  private async ensureReady(): Promise<void> {
    if (this.usable()) return;
    if (this.building) {
      await this.building;
      return;
    }
    // Lazy build once if missing. Failures leave status as "failed".
    if (this.status.status === "missing" || this.status.status === "failed") {
      try {
        await this.reindex();
      } catch {
        // swallow — callers see usable() false and return empty results
      }
    }
  }

  private async buildIndex(): Promise<void> {
    this.status = { status: "building" };
    try {
      await mkdir(this.storageRoot, { recursive: true });
      await writeStatus(this.storageRoot, { status: "building", at: now() });
      const documents: IndexDocument[] = [];
      for await (const file of walkMarkdown(this.docsRoot)) {
        const rel = toPosix(relative(this.docsRoot, file));
        const domain = inferDomain(rel);
        const raw = await readFile(file, "utf8");
        documents.push(parseDocument(rel, raw, domain));
      }
      const index: IndexFile = { documents };
      await writeFile(join(this.storageRoot, INDEX_FILE), JSON.stringify(index), "utf8");
      this.index = index;
      this.status = { status: "ready", at: now(), documentCount: documents.length };
      await writeStatus(this.storageRoot, this.status);
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.status = { status: "failed", message, at: now() };
      await writeStatus(this.storageRoot, this.status).catch(() => undefined);
      throw new ApplicationError("INTERNAL_ERROR", `Docs index build failed: ${message}`);
    }
  }
}

async function* walkMarkdown(root: string): AsyncGenerator<string> {
  const entries = await readdir(root, { withFileTypes: true });
  for (const entry of entries) {
    const full = join(root, entry.name);
    if (entry.isDirectory()) {
      yield* walkMarkdown(full);
    } else if (entry.isFile() && entry.name.endsWith(".md")) {
      yield full;
    }
  }
}

function parseDocument(path: string, raw: string, domain: string): IndexDocument {
  const text = raw.replace(/\r\n/g, "\n");
  const lines = text.split("\n");
  const headings: string[] = [];
  let title = path;
  for (const line of lines) {
    const match = line.match(/^(#{1,3})\s+(.+?)(?:\s+#*)?$/);
    if (match) {
      const headingText = match[2]!.trim();
      headings.push(headingText);
      if (match[1]!.length === 1 && title === path) {
        title = headingText;
      }
    }
  }

  const chunks: IndexChunk[] = [];
  let current: { heading: string; lines: string[] } | null = null;
  let prefaceLines: string[] = [];
  let prefaceFinished = false;
  for (const line of lines) {
    const h2 = line.match(/^##\s+(.+?)(?:\s+#*)?$/);
    if (h2) {
      if (current) {
        chunks.push(finishChunk(current, chunks.length === 0 ? prefaceLines : []));
      }
      current = { heading: h2[1]!.trim(), lines: [line] };
      prefaceFinished = true;
    } else {
      if (current) {
        current.lines.push(line);
      } else if (!prefaceFinished) {
        prefaceLines.push(line);
      }
    }
  }
  if (current) {
    chunks.push(finishChunk(current, chunks.length === 0 ? prefaceLines : []));
  }

  if (chunks.length === 0) {
    chunks.push({ id: "full", heading: title, text });
  } else if (prefaceLines.length > 0 && chunks[0]!.heading) {
    // prepend preface to first H2 chunk so title/front-matter isn't lost
    const first = chunks[0]!;
    chunks[0] = { ...first, text: prefaceLines.join("\n") + "\n" + first.text };
  }

  const slugCounts = new Map<string, number>();
  const withIds: IndexChunk[] = [];
  for (const chunk of chunks) {
    let slug = slugify(chunk.heading) || "chunk";
    const count = slugCounts.get(slug) ?? 0;
    slugCounts.set(slug, count + 1);
    if (count > 0) slug = `${slug}-${count + 1}`;
    withIds.push({ ...chunk, id: slug });
  }

  return { path, title, domain, headings, text, chunks: withIds };
}

function finishChunk(current: { heading: string; lines: string[] }, preface: string[]): IndexChunk {
  const all = preface.length > 0 ? [...preface, ...current.lines] : current.lines;
  // trim leading/trailing blank lines
  let start = 0;
  while (start < all.length && all[start]!.trim() === "") start++;
  let end = all.length;
  while (end > start && all[end - 1]!.trim() === "") end--;
  return { id: "", heading: current.heading, text: all.slice(start, end).join("\n") };
}

function tokenize(text: string): string[] {
  return text
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((t) => t.length > 0);
}

function scoreChunk(chunk: IndexChunk, terms: string[]): number {
  const searchable = `${chunk.heading} ${chunk.text}`.toLowerCase();
  let score = 0;
  for (const term of terms) {
    let idx = searchable.indexOf(term);
    while (idx >= 0) {
      score++;
      idx = searchable.indexOf(term, idx + term.length);
    }
  }
  return score;
}

function makeExcerpt(text: string, terms: string[]): string {
  if (terms.length === 0) return text.slice(0, MAX_EXCERPT);
  const lower = text.toLowerCase();
  let best = -1;
  for (const term of terms) {
    const idx = lower.indexOf(term);
    if (idx >= 0 && (best < 0 || idx < best)) best = idx;
  }
  if (best < 0) return text.slice(0, MAX_EXCERPT);
  const start = Math.max(0, best - 60);
  const end = Math.min(text.length, start + MAX_EXCERPT);
  const prefix = start > 0 ? "..." : "";
  const suffix = end < text.length ? "..." : "";
  return `${prefix}${text.slice(start, end).trim()}${suffix}`;
}

function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64);
}

function inferDomain(relPath: string): string {
  const dir = dirname(relPath);
  return dir === "." ? "root" : dir;
}

function safeRelativePath(input: string): string | undefined {
  const normalized = normalize(input).replace(/\\/g, "/");
  if (isAbsolute(normalized) || normalized.startsWith("..")) return undefined;
  return normalized.replace(/^\/+/, "").replace(/\/+/g, "/");
}

function toPosix(p: string): string {
  return p.replace(/\\/g, "/");
}

async function writeStatus(storageRoot: string, status: StatusMeta): Promise<void> {
  await writeFile(join(storageRoot, STATUS_FILE), JSON.stringify(status), "utf8");
}

function now(): number {
  return Date.now() / 1000;
}
