/**
 * One ranked document chunk returned by a docs search.
 */
export interface DocsHit {
  readonly path: string;
  readonly title: string;
  readonly heading: string;
  readonly chunkId: string;
  readonly excerpt: string;
  readonly score: number;
}

/**
 * Lightweight catalog entry for docs_list.
 */
export interface DocSummary {
  readonly path: string;
  readonly title: string;
  readonly headings: readonly string[];
  readonly domain: string;
}

/**
 * Full document content returned by docs_read.
 */
export interface DocContent {
  readonly path: string;
  readonly title: string;
  readonly headings: readonly string[];
  readonly domain: string;
  readonly text: string;
  readonly chunkId?: string;
  readonly chunk?: string;
}

/**
 * Port for indexing and retrieving the agent-facing documentation corpus.
 */
export interface DocsIndexPort {
  /**
   * Whether the index is built and ready to query.
   */
  usable(): boolean;

  /**
   * Rebuild the index from the docs root. If the index is already up to date,
   * implementations may skip the work.
   */
  reindex(): Promise<void>;

  /**
   * Search the documentation corpus for chunks matching the query.
   * Returns an empty list when the index is not usable.
   */
  search(query: string, topK: number): Promise<readonly DocsHit[]>;

  /**
   * List all indexed documents.
   */
  listDocs(): Promise<readonly DocSummary[]>;

  /**
   * Read a single document by path. If chunkId is provided, only that chunk's
   * text is returned. Returns undefined when the document is not found.
   */
  readDoc(path: string, chunkId?: string): Promise<DocContent | undefined>;
}
