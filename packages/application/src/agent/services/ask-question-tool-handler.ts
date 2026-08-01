import { ApplicationError } from "../../errors/application-error.js";
import type { AskQuestionOption, AskQuestionService } from "./ask-question-service.js";
import { requireString } from "./gateway-utils.js";

const MAX_ASK_OPTIONS = 8;

export async function execAskQuestion(
  askQuestions: AskQuestionService | undefined,
  isInteractive: boolean,
  args: Readonly<Record<string, unknown>>,
  callId: string,
  turnId: string,
): Promise<unknown> {
  if (!askQuestions) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "ask_question is not available in this runtime");
  }
  if (!isInteractive) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "ask_question is only available during interactive agent turns");
  }
  const question = requireString(args.question, "question").trim();
  if (!question) throw new ApplicationError("AGENT_INVALID_INPUT", "question must not be empty");
  const options = parseAskOptions(args.options);
  const allowFreeText = args.allow_free_text === undefined ? true : Boolean(args.allow_free_text);
  const multiSelect = Boolean(args.multi_select);
  return askQuestions.ask(turnId, callId, { question, options, allowFreeText, multiSelect });
}

function parseAskOptions(raw: unknown): AskQuestionOption[] {
  if (!Array.isArray(raw) || raw.length === 0) {
    throw new ApplicationError("AGENT_INVALID_INPUT", "options must be a non-empty array");
  }
  if (raw.length > MAX_ASK_OPTIONS) {
    throw new ApplicationError("AGENT_INVALID_INPUT", `options must contain at most ${MAX_ASK_OPTIONS} items`);
  }
  const seen = new Set<string>();
  const options: AskQuestionOption[] = [];
  for (const entry of raw) {
    if (typeof entry !== "object" || entry === null || Array.isArray(entry)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "each option must be an object");
    }
    const id = requireString(entry.id, "options[].id").trim();
    const label = requireString(entry.label, "options[].label").trim();
    if (!id || !label) {
      throw new ApplicationError("AGENT_INVALID_INPUT", "each option requires non-empty id and label");
    }
    if (seen.has(id)) {
      throw new ApplicationError("AGENT_INVALID_INPUT", `duplicate option id: ${id}`);
    }
    seen.add(id);
    options.push({
      id,
      label,
      ...(typeof entry.description === "string" && entry.description.trim() ? { description: entry.description.trim() } : {}),
      ...(entry.default === true ? { default: true } : {}),
      ...(typeof entry.icon === "string" && entry.icon.trim() ? { icon: entry.icon.trim() } : {}),
      ...(typeof entry.image === "string" && entry.image.trim() ? { image: entry.image.trim() } : {}),
    });
  }
  return options;
}
