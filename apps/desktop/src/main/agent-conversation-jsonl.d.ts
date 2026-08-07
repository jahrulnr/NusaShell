/** Type declarations for the JSONL conversation primitives module. */

export function appendJsonlLine(path: string, record: unknown): Promise<void>;

export function readJsonlLines(path: string): Promise<unknown[]>;

export function countJsonlLines(path: string): Promise<number>;

export function jsonlFileSize(path: string): Promise<number>;
