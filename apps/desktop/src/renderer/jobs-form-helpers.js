/**
 * Pure helpers for the Jobs create/edit form — schema-driven tool arg
 * serialization. Extracted from JobsController so they can be unit-tested
 * without a DOM. The controller still owns rendering; these functions operate
 * on plain field descriptors so the serialization logic is testable in isolation.
 */

/**
 * Serialize a list of schema field descriptors (as produced by
 * `renderSchemaForm`) into an args object.
 *
 * Field descriptor shape:
 *   { key: string, type: "string"|"number"|"boolean"|"enum"|"json", value: string | boolean }
 *
 * Rules:
 *   - string/enum: omitted when empty string
 *   - number: converted to Number, omitted when empty
 *   - boolean: always included (checkbox state)
 *   - json: parsed via JSON.parse; omitted when empty; throws on invalid JSON
 *
 * @param {ReadonlyArray<{key:string,type:string,value:string|boolean}>} fields
 * @returns {Record<string, unknown>}
 */
export function serializeSchemaArgs(fields) {
  const args = {};
  for (const field of fields) {
    const { key, type, value } = field;
    if (type === "json") {
      const raw = String(value).trim();
      if (raw) {
        try {
          args[key] = JSON.parse(raw);
        } catch {
          throw new Error(`Invalid JSON for ${key}`);
        }
      }
    } else if (type === "boolean") {
      args[key] = Boolean(value);
    } else if (type === "number") {
      const v = String(value).trim();
      if (v) args[key] = Number(v);
    } else if (type === "enum") {
      if (value) args[key] = value;
    } else {
      if (value) args[key] = value;
    }
  }
  return args;
}

/**
 * Build a list of field descriptors from a JSON Schema's inputSchema.
 * Used for testing the serialization without a DOM; the controller's
 * `_renderSchemaForm` builds DOM elements with matching `data-schema-type`
 * attributes that `_serializeSchemaForm` reads back.
 *
 * @param {{properties?: Record<string, unknown>, required?: string[]}} schema
 * @param {Record<string, unknown>} prefill
 * @returns {Array<{key:string,type:string,value:string|boolean,required:boolean}>}
 */
export function describeSchemaFields(schema, prefill = {}) {
  const props = schema.properties ?? {};
  const required = new Set(schema.required ?? []);
  const fields = [];
  for (const [key, def] of Object.entries(props)) {
    const d = def ?? {};
    const type = d.type ?? "string";
    const isRequired = required.has(key);
    if (type === "object" || type === "array") {
      const pre = key in prefill ? JSON.stringify(prefill[key], null, 2) : "";
      fields.push({ key, type: "json", value: pre, required: isRequired });
    } else if (d.enum) {
      const pre = key in prefill ? String(prefill[key]) : (d.default ?? "");
      fields.push({ key, type: "enum", value: pre, required: isRequired });
    } else if (type === "boolean") {
      const pre = key in prefill ? Boolean(prefill[key]) : Boolean(d.default);
      fields.push({ key, type: "boolean", value: pre, required: isRequired });
    } else if (type === "number") {
      const pre = key in prefill ? String(prefill[key]) : (d.default !== undefined ? String(d.default) : "");
      fields.push({ key, type: "number", value: pre, required: isRequired });
    } else {
      const pre = key in prefill ? String(prefill[key]) : (d.default ?? "");
      fields.push({ key, type: "string", value: pre, required: isRequired });
    }
  }
  return fields;
}
