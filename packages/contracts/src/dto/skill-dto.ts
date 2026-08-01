/**
 * A staged skill write awaiting user approval.
 *
 * Lives in contracts so the Electron preload boundary can depend on it without
 * pulling in `@nusashell/infrastructure`.
 */
export interface PendingSkillWrite {
  readonly id: string;
  readonly skillId: string;
  readonly action: "create" | "edit" | "write_file" | "delete";
  readonly path: string;
  readonly content: string;
  readonly createdAt: string;
}
