import type { Query } from "../../../messaging/query.js";

export interface ValidateScheduleQuery extends Query {
  readonly kind: "validate-schedule";
  readonly schedule: string;
}

export interface ValidateScheduleResult {
  readonly ok: boolean;
  readonly error?: string;
  readonly description?: string;
}
