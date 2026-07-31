export interface ReviewState {
  readonly turnsSinceMemory: number;
  readonly toolRoundsSinceSkill: number;
  readonly lastReviewAt?: string;
}

export interface ReviewStateStorePort {
  load(): Promise<ReviewState>;
  save(state: ReviewState): Promise<void>;
}
