import { describe, expect, it } from "vitest";
import { serializeSchemaArgs, describeSchemaFields } from "../src/renderer/jobs-form-helpers.js";

describe("serializeSchemaArgs", () => {
  it("serializes string fields, omitting empty values", () => {
    const args = serializeSchemaArgs([
      { key: "name", type: "string", value: "hello" },
      { key: "label", type: "string", value: "" },
    ]);
    expect(args).toEqual({ name: "hello" });
  });

  it("serializes number fields, converting to Number and omitting empty", () => {
    const args = serializeSchemaArgs([
      { key: "count", type: "number", value: "42" },
      { key: "empty", type: "number", value: "" },
    ]);
    expect(args).toEqual({ count: 42 });
    expect(typeof args.count).toBe("number");
  });

  it("serializes boolean fields, always including the value", () => {
    const args = serializeSchemaArgs([
      { key: "enabled", type: "boolean", value: true },
      { key: "disabled", type: "boolean", value: false },
    ]);
    expect(args).toEqual({ enabled: true, disabled: false });
  });

  it("serializes enum fields, omitting empty selection", () => {
    const args = serializeSchemaArgs([
      { key: "mode", type: "enum", value: "fast" },
      { key: "unset", type: "enum", value: "" },
    ]);
    expect(args).toEqual({ mode: "fast" });
  });

  it("serializes json fields, parsing valid JSON", () => {
    const args = serializeSchemaArgs([
      { key: "filters", type: "json", value: '{"to":"x@y.com"}' },
      { key: "tags", type: "json", value: '["a","b"]' },
      { key: "empty", type: "json", value: "" },
    ]);
    expect(args).toEqual({ filters: { to: "x@y.com" }, tags: ["a", "b"] });
  });

  it("throws on invalid JSON for json fields", () => {
    expect(() =>
      serializeSchemaArgs([{ key: "bad", type: "json", value: "{not json" }]),
    ).toThrow(/Invalid JSON for bad/);
  });

  it("handles an empty field list", () => {
    expect(serializeSchemaArgs([])).toEqual({});
  });

  it("trims whitespace before parsing json", () => {
    const args = serializeSchemaArgs([
      { key: "data", type: "json", value: "  {\"k\":1}  " },
    ]);
    expect(args).toEqual({ data: { k: 1 } });
  });
});

describe("describeSchemaFields", () => {
  it("describes primitive, enum, boolean, number, and json fields from a schema", () => {
    const fields = describeSchemaFields({
      properties: {
        name: { type: "string" },
        count: { type: "number" },
        enabled: { type: "boolean" },
        mode: { type: "string", enum: ["fast", "slow"] },
        filters: { type: "object" },
        tags: { type: "array" },
      },
      required: ["name", "count"],
    });
    expect(fields).toEqual([
      { key: "name", type: "string", value: "", required: true },
      { key: "count", type: "number", value: "", required: true },
      { key: "enabled", type: "boolean", value: false, required: false },
      { key: "mode", type: "enum", value: "", required: false },
      { key: "filters", type: "json", value: "", required: false },
      { key: "tags", type: "json", value: "", required: false },
    ]);
  });

  it("prefills values from the prefill object", () => {
    const fields = describeSchemaFields(
      {
        properties: {
          name: { type: "string" },
          count: { type: "number" },
          enabled: { type: "boolean" },
          filters: { type: "object" },
        },
        required: ["name"],
      },
      { name: "test", count: 5, enabled: true, filters: { x: 1 } },
    );
    expect(fields).toEqual([
      { key: "name", type: "string", value: "test", required: true },
      { key: "count", type: "number", value: "5", required: false },
      { key: "enabled", type: "boolean", value: true, required: false },
      { key: "filters", type: "json", value: '{\n  "x": 1\n}', required: false },
    ]);
  });

  it("uses schema defaults when no prefill", () => {
    const fields = describeSchemaFields({
      properties: {
        mode: { type: "string", enum: ["a", "b"], default: "b" },
        count: { type: "number", default: 10 },
        enabled: { type: "boolean", default: true },
      },
    });
    expect(fields).toEqual([
      { key: "mode", type: "enum", value: "b", required: false },
      { key: "count", type: "number", value: "10", required: false },
      { key: "enabled", type: "boolean", value: true, required: false },
    ]);
  });

  it("handles empty schema", () => {
    expect(describeSchemaFields({})).toEqual([]);
    expect(describeSchemaFields({ properties: {} })).toEqual([]);
  });

  it("round-trips: describe then serialize produces the original prefill", () => {
    const prefill = { name: "test", count: 3, enabled: false, mode: "fast", filters: { x: 1 } };
    const schema = {
      properties: {
        name: { type: "string" },
        count: { type: "number" },
        enabled: { type: "boolean" },
        mode: { type: "string", enum: ["fast", "slow"] },
        filters: { type: "object" },
      },
      required: ["name"],
    };
    const fields = describeSchemaFields(schema, prefill);
    const serialized = serializeSchemaArgs(fields);
    expect(serialized).toEqual(prefill);
  });
});
