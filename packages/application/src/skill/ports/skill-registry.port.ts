export interface SkillSummary {
  readonly id: string;
  readonly name: string;
  readonly description: string;
  readonly fileCount: number;
  readonly updatedAt: string;
}

export interface SkillFileEntry {
  readonly path: string;
  readonly type: "file" | "directory";
  readonly sizeBytes: number;
  readonly editable: boolean;
}

export interface SkillDetail extends SkillSummary {
  readonly files: readonly SkillFileEntry[];
}

export interface SkillReadResult {
  readonly skillId: string;
  readonly path: string;
  readonly content?: string;
  readonly sizeBytes: number;
  readonly editable: boolean;
  readonly truncated: boolean;
  readonly nextOffset?: number;
}

export interface SkillRegistryPort {
  list(): Promise<readonly SkillSummary[]>;
  search(query: string, limit?: number): Promise<readonly SkillSummary[]>;
  get(skillId: string): Promise<SkillDetail>;
  read(skillId: string, path?: string, offset?: number, maxChars?: number): Promise<SkillReadResult>;
  installFromArchive(archivePath: string): Promise<SkillDetail>;
  write(skillId: string, path: string, content: string): Promise<SkillReadResult>;
  delete(skillId: string): Promise<void>;
}
