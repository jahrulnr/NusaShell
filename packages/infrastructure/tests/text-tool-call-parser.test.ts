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
