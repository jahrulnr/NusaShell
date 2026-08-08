import { describe, expect, it } from "vitest";
import { extractTextToolCalls, mergeTextToolCalls } from "../src/index.js";

describe("text tool-call recovery", () => {
  it("extracts fenced function calls and coerces scalar arguments", () => {
    const result = extractTextToolCalls(`Before
<function=write_file>
<parameter=append>false</parameter>
<parameter=path>/tmp/a.txt</parameter>
<parameter=count>3</parameter>
</function>
After`);

    expect(result.calls).toEqual([expect.objectContaining({
      name: "write_file",
      args: { append: false, path: "/tmp/a.txt", count: 3 },
    })]);
    expect(result.text).toBe("Before\n\nAfter");
  });

  it("extracts Anthropic invoke and bare tool_use formats", () => {
    const result = extractTextToolCalls(`
<function_calls><invoke name="search"><parameter name="query">mcp</parameter></invoke></function_calls>
<tool_use><name>read_file</name><parameters><path>/tmp/x</path></parameters></tool_use>`);

    expect(result.calls.map((call) => [call.name, call.args])).toEqual([
      ["search", { query: "mcp" }],
      ["read_file", { path: "/tmp/x" }],
    ]);
  });

  it("extracts ASCII and Unicode Kimi pipe-delimited calls", () => {
    const result = extractTextToolCalls(`
<|tool_calls_section_begin|><|tool_call_begin|>functions.search:0<|tool_call_argument_begin|>{"query":"one"}<|tool_call_end|><|tool_calls_section_end|>
<｜tool▁calls▁begin｜><｜tool▁call▁begin｜>Glob<｜tool▁sep｜>pattern
*.ts<｜tool▁call▁end｜><｜tool▁calls▁end｜>`);

    expect(result.calls.map((call) => [call.name, call.args])).toEqual([
      ["search", { query: "one" }],
      ["Glob", { pattern: "*.ts" }],
    ]);
  });

  it("extracts DeepSeek V4 DSML tool_calls (unicode pipes normalized)", () => {
    // Fullwidth pipes as DeepSeek special tokens emit (｜ = U+FF5C).
    const raw = `Thinking done.
<｜DSML｜tool_calls>
<｜DSML｜invoke name="list_tickets">
<｜DSML｜parameter name="project_id" string="true">proj-1</｜DSML｜parameter>
<｜DSML｜parameter name="limit" string="false">25</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜tool_calls>`;
    const result = extractTextToolCalls(raw);
    expect(result.calls).toEqual([expect.objectContaining({
      name: "list_tickets",
      args: { project_id: "proj-1", limit: 25 },
    })]);
    expect(result.text).toBe("Thinking done.");
  });

  it("strips leaked tool_result echoes and orphan DSML closers from assistant text", () => {
    // Regression: DeepSeek-v4-flash sometimes echoes prior MCP envelopes +
    // half-detokenized DSML into the user-visible assistant stream.
    const leaked = `屋</tool_result>Bdy_S
<tool_result>{"ok":true,"data":{"items":[{"id":"2ddb51ed-1de5-4df1-8f2b-ae63661275f7","title":"MCP parallel"}]}}</tool_result>
</|DSML|parameter>
</|DSML|invoke>
</|DSML|tool_calls>
Next steps for the user.`;
    const result = extractTextToolCalls(leaked);
    expect(result.calls).toEqual([]);
    expect(result.text).toBe("Next steps for the user.");
    expect(result.text).not.toContain("tool_result");
    expect(result.text).not.toContain("DSML");
    expect(result.text).not.toContain("2ddb51ed");
  });

  it("strips known model sentence control tokens without removing visible prose", () => {
    const result = extractTextToolCalls("<|begin_of_sentence|>Answer<｜end_of_sentence｜>");
    expect(result.calls).toEqual([]);
    expect(result.text).toBe("Answer");
  });

  it("fills empty native arguments without replacing valid native arguments", () => {
    const merged = mergeTextToolCalls(
      [
        { id: "native-1", name: "search", args: {} },
        { id: "native-2", name: "read_file", args: { path: "/native" } },
      ],
      `<function=search><parameter=query>fallback</parameter></function>
<function=read_file><parameter=path>/text</parameter></function>`,
    );

    expect(merged.calls).toEqual([
      { id: "native-1", name: "search", args: { query: "fallback" } },
      { id: "native-2", name: "read_file", args: { path: "/native" } },
    ]);
  });
});
