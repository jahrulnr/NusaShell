const KNOWN_STATUS_TONES = new Set([
  'candidate',
  'experimental',
  'validated',
  'trusted',
  'deprecated',
  'retired',
]);

const SKILL_FILE_MIME_TYPES = Object.freeze({
  md: 'text/markdown',
  markdown: 'text/markdown',
  mdx: 'text/markdown',
  txt: 'text/plain',
  text: 'text/plain',
  json: 'application/json',
  jsonl: 'application/jsonl',
  yaml: 'application/yaml',
  yml: 'application/yaml',
  toml: 'application/toml',
  xml: 'application/xml',
  html: 'text/html',
  htm: 'text/html',
  css: 'text/css',
  js: 'text/javascript',
  mjs: 'text/javascript',
  cjs: 'text/javascript',
  ts: 'text/typescript',
  tsx: 'text/typescript',
  jsx: 'text/javascript',
  py: 'text/x-python',
  go: 'text/x-go',
  rs: 'text/x-rust',
  java: 'text/x-java',
  c: 'text/x-c',
  h: 'text/x-c',
  cc: 'text/x-c++',
  cpp: 'text/x-c++',
  hpp: 'text/x-c++',
  sh: 'application/x-sh',
  bash: 'application/x-sh',
  zsh: 'application/x-sh',
  fish: 'application/x-sh',
  ps1: 'text/x-powershell',
  bat: 'text/x-batch',
  cmd: 'text/x-batch',
  sql: 'application/sql',
  csv: 'text/csv',
  svg: 'image/svg+xml',
  ini: 'text/plain',
  conf: 'text/plain',
  env: 'text/plain',
  lock: 'text/plain',
  gz: 'application/gzip',
  bz2: 'application/x-bzip2',
  xz: 'application/x-xz',
  zip: 'application/zip',
  tar: 'application/x-tar',
  '7z': 'application/x-7z-compressed',
  rar: 'application/vnd.rar',
  pdf: 'application/pdf',
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  avif: 'image/avif',
  ico: 'image/x-icon',
  mp3: 'audio/mpeg',
  wav: 'audio/wav',
  ogg: 'audio/ogg',
  mp4: 'video/mp4',
  webm: 'video/webm',
  mov: 'video/quicktime',
  wasm: 'application/wasm',
  exe: 'application/vnd.microsoft.portable-executable',
  dll: 'application/vnd.microsoft.portable-executable',
  so: 'application/octet-stream',
  bin: 'application/octet-stream',
  db: 'application/octet-stream',
  sqlite: 'application/octet-stream',
  sqlite3: 'application/octet-stream',
});

const BINARY_SKILL_FILE_EXTENSIONS = new Set([
  'gz', 'bz2', 'xz', 'zip', 'tar', '7z', 'rar', 'pdf',
  'png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'ico',
  'mp3', 'wav', 'ogg', 'mp4', 'webm', 'mov', 'wasm',
  'exe', 'dll', 'so', 'bin', 'db', 'sqlite', 'sqlite3',
]);

function skillFileExtension(path) {
  const name = String(path || '').replaceAll('\\', '/').split('/').pop() || '';
  const match = /\.([^.]+)$/.exec(name);
  return match ? match[1].toLowerCase() : '';
}

export function detectSkillFileMime(path) {
  const extension = skillFileExtension(path);
  return SKILL_FILE_MIME_TYPES[extension] || 'text/plain';
}

export function isBinarySkillFile(path, content = '') {
  return BINARY_SKILL_FILE_EXTENSIONS.has(skillFileExtension(path))
    || String(content || '').includes('\u0000');
}

export function filterSkills(skills = [], query = '') {
  const needle = String(query || '').trim().toLowerCase();
  if (!needle) return [...skills];
  return skills.filter((skill) => [
    skill?.id,
    skill?.name,
    skill?.description,
    skill?.category,
    skill?.owned_by,
    skill?.origin,
  ].some((value) => String(value || '').toLowerCase().includes(needle)));
}

export function skillStatusTone(skill) {
  const status = String(skill?.status || 'trusted').trim().toLowerCase();
  return KNOWN_STATUS_TONES.has(status) ? status : 'trusted';
}

export function skillStatusLabel(skill) {
  const tone = skillStatusTone(skill);
  return tone[0].toUpperCase() + tone.slice(1);
}

export function skillOwnerLabel(skillOrOwner) {
  const owner = typeof skillOrOwner === 'object'
    ? String(skillOrOwner?.owned_by || skillOrOwner?.origin || '').trim()
    : String(skillOrOwner || '').trim();
  if (!owner) return 'Unknown owner';
  if (owner === 'builtin') return 'Built-in';
  if (owner === 'user') return 'User';
  if (owner === 'learned') return 'Learned';
  if (owner.startsWith('plugin:')) return `Plugin · ${owner.slice('plugin:'.length)}`;
  return owner;
}

export function skillOwnerTone(skill) {
  const owner = String(skill?.owned_by || skill?.origin || '').trim();
  if (owner.startsWith('plugin:')) return 'plugin';
  if (owner === 'learned') return 'learned';
  if (owner === 'user') return 'user';
  if (owner === 'builtin') return 'builtin';
  return 'unknown';
}

export function skillVersionLabel(skill) {
  const version = Number(skill?.version) || 1;
  const active = Number(skill?.active_version) || version;
  return active !== version ? `v${active} · latest v${version}` : `v${version}`;
}

export function versionOptions(skill) {
  const listed = skill?.versions || skill?.history || skill?.revisions || [];
  const values = [];
  if (Array.isArray(listed) && listed.length) {
    for (const item of listed) {
      const value = typeof item === 'object' ? item.version : item;
      const version = Number(value);
      if (Number.isInteger(version) && version > 0 && !values.includes(version)) values.push(version);
    }
  }
  if (!values.length) {
    const latest = Math.max(1, Number(skill?.version) || 1);
    for (let version = 1; version <= latest; version += 1) values.push(version);
  }
  values.sort((a, b) => b - a);
  return values.map((version) => ({ text: `v${version}`, value: String(version) }));
}

export function formatSkillFileSize(bytes) {
  const size = Math.max(0, Number(bytes) || 0);
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

export function formatSkillDate(value) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return 'Never';
  return date.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' });
}

export function stripSkillFrontMatter(content = '') {
  const source = String(content ?? '').replace(/^\uFEFF/, '');
  const frontMatter = source.match(/^---\s*\r?\n[\s\S]*?\r?\n---\s*(?:\r?\n|$)/);
  return frontMatter ? source.slice(frontMatter[0].length).trimStart() : source;
}

export function buildSkillFileTree(entries = []) {
  const root = { name: '', path: '', type: 'directory', children: [] };

  for (const entry of entries) {
    const normalized = String(entry?.path || '').replaceAll('\\', '/').replace(/^\/+|\/+$/g, '');
    if (!normalized) continue;
    const parts = normalized.split('/').filter(Boolean);
    let parent = root;
    let path = '';
    parts.forEach((name, index) => {
      path = path ? `${path}/${name}` : name;
      const isFile = index === parts.length - 1 && entry?.type === 'file';
      let node = parent.children.find((candidate) => candidate.path === path);
      if (!node) {
        node = {
          name,
          path,
          type: isFile ? 'file' : 'directory',
          sizeBytes: isFile ? Number(entry.sizeBytes) || 0 : 0,
          editable: isFile ? Boolean(entry.editable) : false,
          children: [],
        };
        parent.children.push(node);
      } else if (isFile) {
        node.type = 'file';
        node.sizeBytes = Number(entry.sizeBytes) || 0;
        node.editable = Boolean(entry.editable);
      }
      parent = node;
    });
  }

  const sort = (nodes) => {
    nodes.sort((left, right) => {
      if (left.type !== right.type) return left.type === 'directory' ? -1 : 1;
      return left.name.localeCompare(right.name);
    });
    nodes.forEach((node) => sort(node.children));
  };
  sort(root.children);
  return root.children;
}
