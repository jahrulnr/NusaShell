import { rpc } from '../../rpc.js';

// The catalog is process-local UI state. Tool cards still render when the
// catalog request fails because persisted conversations may outlive the
// backend version that produced them.
const SUPPORTED_VERSION = 1;
let catalogVersion = 0;
let catalog = new Map();
let catalogLoadGeneration = 0;

function isObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function validCSSClass(value) {
  return typeof value === 'string' && /^[A-Za-z][A-Za-z0-9_-]*$/.test(value);
}

function fallbackSlug(name) {
  const raw = String(name || 'tool').trim().toLowerCase();
  const normalized = raw.startsWith('mcp__') ? 'mcp' : raw;
  const chars = [];
  let lastDash = false;
  for (const char of normalized) {
    if ((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
      chars.push(char);
      lastDash = false;
    } else if ((char === '_' || char === '-' || char === ' ') && chars.length && !lastDash) {
      chars.push('-');
      lastDash = true;
    }
  }
  return chars.join('').replace(/^-+|-+$/g, '') || 'tool';
}

function fallbackRef(name) {
  const normalized = String(name || 'tool').trim() || 'tool';
  return {
    id: `tool.${normalized}.v1`,
    version: SUPPORTED_VERSION,
    css_class: `agent-tool-${fallbackSlug(normalized)}`,
  };
}

function normalizeRef(value, fallback) {
  if (!isObject(value)) return fallback;
  const ref = {
    id: typeof value.id === 'string' && value.id ? value.id : fallback.id,
    version: Number.isInteger(value.version) ? value.version : fallback.version,
    css_class: validCSSClass(value.css_class) ? value.css_class : fallback.css_class,
  };
  return ref.version === SUPPORTED_VERSION ? ref : fallback;
}

function normalizeCatalogEntry(entry) {
  if (!isObject(entry) || typeof entry.name !== 'string' || !entry.name.trim()) return null;
  if (typeof entry.id !== 'string' || !entry.id || entry.version !== SUPPORTED_VERSION || !validCSSClass(entry.css_class)) return null;
  const presentation = isObject(entry.presentation) ? entry.presentation : {};
  const variants = Array.isArray(presentation.variants) ? presentation.variants.filter((v) => typeof v === 'string' && v) : [];
  const formats = Array.isArray(presentation.formats) ? presentation.formats.filter((v) => typeof v === 'string' && v) : [];
  if (!variants.length || !formats.length) return null;
  return {
    name: entry.name,
    description: typeof entry.description === 'string' ? entry.description : '',
    id: entry.id,
    version: entry.version,
    css_class: entry.css_class,
    input_schema: isObject(entry.input_schema) ? entry.input_schema : {},
    presentation: {
      variants,
      formats,
      request_fields: Array.isArray(presentation.request_fields) ? presentation.request_fields.filter((v) => typeof v === 'string') : [],
      result_fields: Array.isArray(presentation.result_fields) ? presentation.result_fields.filter((v) => typeof v === 'string') : [],
      attachment_types: Array.isArray(presentation.attachment_types) ? presentation.attachment_types.filter((v) => typeof v === 'string') : [],
    },
  };
}

// registerToolContracts validates the backend catalog once at the boundary;
// renderers consume normalized entries and do not need to guess field names.
export function registerToolContracts(result) {
  if (!isObject(result) || result.version !== SUPPORTED_VERSION || !Array.isArray(result.tools)) {
    throw new Error('Unsupported built-in tool contract catalog');
  }
  const next = new Map();
  for (const raw of result.tools) {
    const entry = normalizeCatalogEntry(raw);
    if (entry) next.set(entry.name, entry);
  }
  catalogVersion = result.version;
  catalog = next;
  return [...catalog.values()];
}

export async function loadToolContracts(workspace = '') {
  const generation = ++catalogLoadGeneration;
  try {
    const result = await rpc('agent.tools.contracts', { workspace: workspace || '' });
    if (generation !== catalogLoadGeneration) return [...catalog.values()];
    return registerToolContracts(result);
  } catch (error) {
    if (generation === catalogLoadGeneration) {
      catalogVersion = 0;
      catalog = new Map();
    }
    throw error;
  }
}

export function toolContractFor(name) {
  return catalog.get(String(name || '')) || null;
}

export function toolContractVersion() {
  return catalogVersion;
}

export function toolContractRef(name, presentation = null) {
  const fallback = fallbackRef(name);
  return normalizeRef(presentation?.contract, toolContractFor(name) || fallback);
}

export function toolContractClass(name, presentation = null) {
  return toolContractRef(name, presentation).css_class;
}

function normalizedAttachments(source, result) {
  if (Array.isArray(result?.attachments)) return result.attachments;
  if (Array.isArray(source?.output_attachments)) return source.output_attachments;
  if (Array.isArray(source?.attachments)) return source.attachments;
  return [];
}

// normalizeToolCall is the one compatibility seam for snapshots, lifecycle
// events, and live frames. New renderers prefer result.attachments while old
// top-level output_attachments/attachments remain accepted.
export function normalizeToolCall(toolCall = {}) {
  const source = isObject(toolCall) ? toolCall : {};
  const name = String(source.name || 'tool');
  const rawPresentation = isObject(source.presentation) ? source.presentation : null;
  const attachments = normalizedAttachments(source, rawPresentation?.result);
  const presentation = rawPresentation
    ? {
        ...rawPresentation,
        contract: toolContractRef(name, rawPresentation),
        result: isObject(rawPresentation.result)
          ? { ...rawPresentation.result, ...(Array.isArray(rawPresentation.result.attachments) ? {} : { attachments }) }
          : { attachments },
      }
    : null;
  const normalized = { ...source, name };
  if (presentation) normalized.presentation = presentation;
  if (attachments.length && (!Array.isArray(normalized.output_attachments) || normalized.output_attachments.length === 0)) {
    normalized.output_attachments = attachments;
  }
  return normalized;
}
