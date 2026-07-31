import type { QueryHandler } from "../../../messaging/query-handler.js";
import { parseSchedule, describeSchedule, ScheduleParseError } from "../../schedule-parser.js";
import type { ValidateScheduleQuery, ValidateScheduleResult } from "./validate-schedule.query.js";

export class ValidateScheduleHandler implements QueryHandler<ValidateScheduleQuery, ValidateScheduleResult> {
  async handle(query: ValidateScheduleQuery): Promise<ValidateScheduleResult> {
    try {
      const schedule = parseSchedule(query.schedule);
      return { ok: true, description: describeSchedule(schedule) };
    } catch (error) {
      if (error instanceof ScheduleParseError) {
        return { ok: false, error: error.message };
      }
      return { ok: false, error: error instanceof Error ? error.message : String(error) };
    }
  }
}
