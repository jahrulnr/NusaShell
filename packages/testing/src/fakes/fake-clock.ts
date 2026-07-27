import type { ClockPort } from "@nusashell/application";

export class FakeClock implements ClockPort {
  private current: Date;

  constructor(initial: Date = new Date("2026-01-01T00:00:00Z")) {
    this.current = initial;
  }

  now(): Date {
    return this.current;
  }

  advance(ms: number): void {
    this.current = new Date(this.current.getTime() + ms);
  }
}
