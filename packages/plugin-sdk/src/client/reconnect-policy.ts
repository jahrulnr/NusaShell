export interface ReconnectOptions {
  readonly enabled: boolean;
  readonly maxAttempts: number;
  readonly initialDelayMs: number;
  readonly maxDelayMs: number;
  readonly backoffFactor: number;
  readonly jitterMs: number;
}

export const DEFAULT_RECONNECT_OPTIONS: ReconnectOptions = {
  enabled: true,
  maxAttempts: Infinity,
  initialDelayMs: 1000,
  maxDelayMs: 30000,
  backoffFactor: 2,
  jitterMs: 500,
};

export interface ReconnectState {
  readonly attempt: number;
  readonly exhausted: boolean;
}

export class ReconnectPolicy {
  private attempt = 0;
  private exhausted = false;
  private readonly options: ReconnectOptions;

  constructor(options: Partial<ReconnectOptions> = {}) {
    this.options = { ...DEFAULT_RECONNECT_OPTIONS, ...options };
  }

  shouldRetry(): boolean {
    if (!this.options.enabled) return false;
    if (this.exhausted) return false;
    return this.attempt < this.options.maxAttempts;
  }

  getDelay(): number {
    const base = Math.min(
      this.options.initialDelayMs * Math.pow(this.options.backoffFactor, this.attempt),
      this.options.maxDelayMs,
    );
    const jitter = (Math.random() - 0.5) * 2 * this.options.jitterMs;
    return Math.max(0, Math.round(base + jitter));
  }

  recordAttempt(): void {
    this.attempt += 1;
    if (this.attempt >= this.options.maxAttempts) {
      this.exhausted = true;
    }
  }

  reset(): void {
    this.attempt = 0;
    this.exhausted = false;
  }

  get state(): ReconnectState {
    return { attempt: this.attempt, exhausted: this.exhausted };
  }

  get isExhausted(): boolean {
    return this.exhausted;
  }

  get currentAttempt(): number {
    return this.attempt;
  }
}
