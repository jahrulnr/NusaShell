export type SkillOrigin = "agent" | "user";

export interface SkillProvenanceEntry {
  readonly createdBy: SkillOrigin;
  readonly createdAt: string;
}

export interface SkillProvenancePort {
  get(skillId: string): Promise<SkillOrigin>;
  markAgent(skillId: string): Promise<void>;
  markUser(skillId: string): Promise<void>;
  clear(skillId: string): Promise<void>;
}
