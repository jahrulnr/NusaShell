#!/usr/bin/env python3
"""Validate a skill folder (SKILL.md) for NusaShell-compatible front matter.

Usage:
    python3 lint_skill.py <path-to-skill-folder> [--strict]

Exit code 0 = pass (warnings allowed), 1 = lint errors.
Stdlib only.
"""
import sys
import re
from pathlib import Path

MAX_NAME_CHARS = 64
MAX_DESCRIPTION_CHARS = 1024
MAX_BODY_LINES = 500
MAX_REFERENCE_LINES = 300

NON_NUSASHELL_TOOL_HINTS = [
    "vault_search", "memory_expand", "knowledge_graph_search", "memory_get",
    "skill_manage", "publish_skill", "use_skill", "skill_search",
    "delegate_task", "web_extract", "openclaw skills", "clawhub",
    "~/.goclaw", "~/.openclaw", "$HERMES_HOME", "~/.hermes",
    "~/.codex/skills", "~/.claude/skills", "terminal(",
]

EXTERNAL_PATH_HINTS = [
    r"\.experimental/(goclaw|hermes-agent|openclaw-main|codex)",
    r"codex-rs",
]

def lint_skill(path: Path, strict: bool = False) -> bool:
    errors, warnings = [], []
    skill_md = path / "SKILL.md"
    if not skill_md.exists():
        errors.append(f"missing SKILL.md in {path}")
        return False
    raw = skill_md.read_text(encoding="utf-8", errors="replace")

    if not raw.startswith("---"):
        errors.append("SKILL.md must start with '---' (front matter)")
    else:
        parts = raw.split("---", 2)
        if len(parts) < 3:
            errors.append("unterminated front matter (need closing '---')")
        else:
            import re as _re
            fm = parts[1]

            def yaml_field(name):
                m = _re.search(rf"^{name}:\s*(.*)$", fm, _re.M)
                return m.group(1).strip().strip('"\'') if m else None

            name = yaml_field("name")
            desc = yaml_field("description")
            if not name:
                errors.append("front matter missing 'name'")
            else:
                if not _re.fullmatch(r"[a-z0-9][a-z0-9-]*", name):
                    errors.append(f"name '{name}' must be lowercase-hyphen")
                if len(name) > MAX_NAME_CHARS:
                    errors.append(f"name longer than {MAX_NAME_CHARS} chars")
            if desc is None:
                errors.append("front matter missing 'description'")
            elif len(desc) > MAX_DESCRIPTION_CHARS:
                errors.append(f"description longer than {MAX_DESCRIPTION_CHARS} chars")
            body = parts[2].strip()
            if not body:
                errors.append("empty body after front matter")

    lines = raw.splitlines()
    if len(lines) > MAX_BODY_LINES:
        warnings.append(f"SKILL.md has {len(lines)} lines (target < {MAX_BODY_LINES})")

    # tool hints scan — body only (front matter metadata.source legitimately names source repos)
    body_text = raw
    if raw.startswith("---"):
        parts = raw.split("---", 2)
        if len(parts) == 3:
            body_text = parts[2]
    for hint in NON_NUSASHELL_TOOL_HINTS:
        if hint in body_text:
            warnings.append(f"possible external tool reference: '{hint}' — map to NusaShell equivalents")
    for hint in EXTERNAL_PATH_HINTS:
        if re.search(hint, body_text):
            warnings.append(f"possible external repo path reference: {hint!r}")

    # references size check
    refs_dir = path / "references"
    if refs_dir.is_dir():
        for ref in refs_dir.glob("*.md"):
            n = len(ref.read_text(encoding="utf-8", errors="replace").splitlines())
            if n > MAX_REFERENCE_LINES:
                warnings.append(f"references/{ref.name} has {n} lines (target < {MAX_REFERENCE_LINES})")

    print(f"== {path}")
    for e in errors:
        print(f"  [ERROR]   {e}")
    for w in warnings:
        print(f"  [WARNING] {w}")
    print(f"  -> {'FAIL' if errors else 'PASS'} ({len(warnings)} warnings)")
    if strict and warnings:
        return False
    return not errors


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(2)
    strict = "--strict" in sys.argv
    results = [lint_skill(Path(p), strict) for p in sys.argv[1:] if p != "--strict"]
    sys.exit(0 if all(results) else 1)


if __name__ == "__main__":
    main()