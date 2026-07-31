export type SkillState = "active" | "stale" | "archived";

export type UsageBumpKind = "use" | "view" | "patch";

export interface SkillUsageRecord {
  readonly skillId: string;
  readonly useCount: number;
  readonly lastUsedAt: string | null;
  readonly viewCount: number;
  readonly lastViewedAt: string | null;
  readonly patchCount: number;
  readonly lastPatchedAt: string | null;
  readonly createdAt: string;
  readonly state: SkillState;
  readonly pinned: boolean;
  readonly archivedAt: string | null;
}

export interface SkillUsagePort {
  record(skillId: string, kind: UsageBumpKind): Promise<void>;
  getRecord(skillId: string): Promise<SkillUsageRecord>;
  listRecords(): Promise<readonly SkillUsageRecord[]>;
  setState(skillId: string, state: SkillState): Promise<void>;
  setPinned(skillId: string, pinned: boolean): Promise<void>;
  clear(skillId: string): Promise<void>;
}

export function latestActivityAt(record: SkillUsageRecord): string {
  const candidates = [record.lastUsedAt, record.lastViewedAt, record.lastPatchedAt]
    .filter((value): value is string => value !== null);
  if (candidates.length === 0) return record.createdAt;
  return candidates.reduce((latest, current) =>
    current > latest ? current : latest,
  );
}
