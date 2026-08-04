export type SkillOrigin = "agent" | "user" | "builtin";

export interface SkillProvenanceEntry {
  readonly createdBy: SkillOrigin;
  readonly createdAt: string;
}

export interface SkillProvenancePort {
  get(skillId: string): Promise<SkillOrigin>;
  markAgent(skillId: string): Promise<void>;
  markUser(skillId: string): Promise<void>;
  readonly markBuiltin?: (skillId: string) => Promise<void>;
  clear(skillId: string): Promise<void>;
  /**
   * Mark a builtin skill as intentionally deleted by the user so it is not
   * re-seeded on the next startup. Optional — implementations that do not
   * support builtin deletion tracking should leave this undefined.
   */
  readonly markBuiltinDeleted?: (skillId: string) => Promise<void>;
  /**
   * Remove a skill from the deleted-builtin list so it can be re-seeded again.
   * Optional — the inverse of `markBuiltinDeleted`.
   */
  readonly unmarkBuiltinDeleted?: (skillId: string) => Promise<void>;
}
