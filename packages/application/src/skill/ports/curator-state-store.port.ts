/**
 * Persistence surface for the {@link SkillCuratorScheduler}'s last-run state.
 * Implemented by an infrastructure adapter so the application layer stays free
 * of `node:fs` imports.
 */
export interface CuratorStateStorePort {
  load(): Promise<{ lastRunAt: string | null }>;
  save(state: { lastRunAt: string | null }): Promise<void>;
}
