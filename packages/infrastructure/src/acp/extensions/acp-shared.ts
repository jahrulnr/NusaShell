import type { AcpPlanStep } from "@nusashell/application";

export function parsePlanSteps(entries: unknown): readonly AcpPlanStep[] {
  if (!Array.isArray(entries)) return [];
  const result: AcpPlanStep[] = [];
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    if (typeof entry !== "object" || entry === null) continue;
    const s = entry as Record<string, unknown>;
    const text = String(s.content ?? s.text ?? s.description ?? "");
    if (!text) continue;
    const status = String(s.status ?? "pending");
    result.push({ id: String(s.id ?? `step_${i}`), text, done: status === "completed" || status === "done" });
  }
  return result;
}
