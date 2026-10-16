import { loadHljs } from '../../js/highlight-render.js?v=2';

const SHELL_ALIASES = new Map([
  ['bash', 'bash'], ['sh', 'bash'], ['zsh', 'bash'], ['fish', 'bash'], ['shell', 'bash'],
  ['powershell', 'powershell'], ['powershell.exe', 'powershell'], ['pwsh', 'powershell'], ['pwsh.exe', 'powershell'], ['ps', 'powershell'],
  ['cmd', 'dos'], ['cmd.exe', 'dos'], ['command.com', 'dos'], ['dos', 'dos'], ['batch', 'dos'],
]);

const POWERSHELL_KEYWORDS = [
  'begin', 'break', 'catch', 'class', 'continue', 'data', 'do', 'dynamicparam', 'else', 'elseif', 'end',
  'exit', 'filter', 'finally', 'for', 'foreach', 'from', 'function', 'if', 'in', 'inlineScript', 'param',
  'process', 'return', 'switch', 'throw', 'trap', 'try', 'until', 'using', 'var', 'while', 'workflow',
].join(' ');

const POWERSHELL_BUILTINS = [
  'Add-Content', 'Add-Type', 'Clear-Content', 'Clear-Host', 'Compare-Object', 'Copy-Item', 'Get-ChildItem',
  'Get-Content', 'Get-Location', 'Get-Process', 'Get-Service', 'Get-Variable', 'Invoke-Command', 'Join-Path',
  'New-Item', 'Remove-Item', 'Resolve-Path', 'Set-Content', 'Set-Location', 'Set-Variable', 'Start-Process',
  'Stop-Process', 'Test-Path', 'Where-Object', 'Write-Error', 'Write-Host', 'Write-Output',
].join(' ');

const DOS_KEYWORDS = [
  'call', 'cd', 'cls', 'copy', 'del', 'dir', 'do', 'echo', 'else', 'endlocal', 'exit', 'for', 'goto', 'if',
  'md', 'mkdir', 'move', 'pause', 'popd', 'pushd', 'rd', 'ren', 'set', 'setlocal', 'shift', 'start', 'type',
].join(' ');

let registeredHighlighter = null;

function powershellLanguage() {
  return {
    name: 'PowerShell',
    aliases: ['ps', 'ps1', 'pwsh'],
    case_insensitive: true,
    keywords: {
      $pattern: /-?[A-Za-z_][\w-]*/,
      keyword: POWERSHELL_KEYWORDS,
      built_in: POWERSHELL_BUILTINS,
      literal: 'true false null',
    },
    contains: [
      { scope: 'meta', begin: /^\s*#requires\b/i, end: /$/ },
      { scope: 'comment', begin: /^\s*#/, end: /$/ },
      { scope: 'built_in', begin: new RegExp(`\\b(?:${POWERSHELL_BUILTINS.split(' ').join('|')})\\b`, 'i') },
      { scope: 'variable', begin: /\$[A-Za-z_][\w]*(?::[A-Za-z_][\w]*)*/ },
      { scope: 'string', variants: [{ begin: /"/, end: /"/ }, { begin: /'/, end: /'/ }] },
      { scope: 'number', begin: /\b\d+(?:\.\d+)?\b/ },
      { scope: 'operator', begin: /&&|\|\||[|;&=<>!+*/%?-]/ },
    ],
  };
}

function dosLanguage() {
  return {
    name: 'Batch',
    aliases: ['bat', 'cmd', 'cmd.exe', 'dos'],
    case_insensitive: true,
    keywords: {
      $pattern: /[A-Za-z][\w-]*/,
      keyword: DOS_KEYWORDS,
      built_in: 'assoc attrib choice color find findstr format help ipconfig ping setx taskkill timeout where',
      literal: 'on off',
    },
    contains: [
      { scope: 'comment', begin: /^\s*(?:@?rem\b|::)/i, end: /$/ },
      { scope: 'variable', begin: /%[^%\r\n]+%|![^!\r\n]+!/ },
      { scope: 'string', begin: /"/, end: /"/ },
      { scope: 'number', begin: /\b\d+\b/ },
      { scope: 'operator', begin: /&&|\|\||[|&<>=^]/ },
    ],
  };
}

function registerCommandLanguages(hljs) {
  if (!hljs || typeof hljs.registerLanguage !== 'function' || registeredHighlighter === hljs) return;
  if (!hljs.getLanguage?.('powershell')) hljs.registerLanguage('powershell', powershellLanguage);
  if (!hljs.getLanguage?.('dos')) hljs.registerLanguage('dos', dosLanguage);
  registeredHighlighter = hljs;
}

function shellAlias(value) {
  const raw = String(value || '').trim().toLowerCase();
  if (!raw) return '';
  const executable = raw.replace(/\\/g, '/').split('/').at(-1)?.split(/\s+/, 1)[0] || raw;
  return SHELL_ALIASES.get(executable) || '';
}

export function commandLanguage(args = {}, command = '') {
  for (const key of ['shell', 'shell_type', 'shellType', 'executable', 'interpreter']) {
    const explicit = shellAlias(args?.[key]);
    if (explicit) return explicit;
  }

  const source = String(command || '').trim();
  const first = source.replace(/^["']/, '').split(/\s+/, 1)[0];
  const prefix = shellAlias(first);
  if (prefix) return prefix;
  if (/\b(?:Get|Set|New|Remove|Write|Invoke|Start|Stop|Test|Convert|Join|Where|ForEach)-[A-Za-z][\w-]*/i.test(source)
    || /\$env:[A-Za-z_][\w]*/i.test(source)) return 'powershell';
  if (/%[A-Za-z_][\w]*%|![A-Za-z_][\w]*!/.test(source)
    || /^\s*@?(?:echo|set|if|for|call)\b/im.test(source)) return 'dos';
  return 'bash';
}

function escapeHTML(value) {
  return String(value || '').replace(/[&<>"']/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[character]));
}

export async function highlightCommand(command, args = {}) {
  const source = String(command || '');
  const language = commandLanguage(args, source);
  if (!source) return { html: '', language, highlighted: false };
  try {
    const hljs = await loadHljs();
    registerCommandLanguages(hljs);
    if (typeof hljs?.highlight !== 'function') throw new Error('highlight.js API unavailable');
    const result = hljs.highlight(source, { language, ignoreIllegals: true });
    return { html: result.value, language, highlighted: true };
  } catch {
    return { html: escapeHTML(source), language, highlighted: false };
  }
}
