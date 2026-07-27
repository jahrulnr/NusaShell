import type { ClockPort } from "@nusashell/application";

export class SystemClock implements ClockPort {
  now(): Date {
    return new Date();
  }
}
