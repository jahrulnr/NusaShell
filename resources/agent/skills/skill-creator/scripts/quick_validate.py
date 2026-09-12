#!/usr/bin/env python3
"""Validate a NusaShell-compatible agent skill package.

Usage:
    python3 quick_validate.py [--strict] [--check-links] [--json] <skill-dir>...

The validator intentionally uses only the Python standard library.  It checks
the package contract and catches unfinished scaffolding; it does not evaluate
whether the instructions produce good agent behavior.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Sequence, Tuple, Union


MAX_SKILL_NAME_LENGTH = 64
MAX_DESCRIPTION_LENGTH = 1024
MAX_SKILL_BODY_LINES = 500
MAX_REFERENCE_LINES = 300
MAX_SKILL_EDITABLE_BYTES = 1024 * 1024

ALLOWED_FRONTMATTER_KEYS = {
    "name",
    "description",
    "compatibility",
    "license",
    "requirements",
    "metadata",
    # Accepted for importing/checking Codex-style packages. NusaShell keeps
    # the field as metadata but does not enforce it at runtime.
    "allowed-tools",
}
ALLOWED_SUPPORT_DIRS = {"references", "templates", "scripts", "assets"}
ALLOWED_ROOT_FILES = {
    "SKILL.md",
    "VERSION",
    "LICENSE",
    "LICENSE.md",
    "LICENSE.txt",
    "NOTICE",
    "NOTICE.md",
}
INTERNAL_ROOT_ENTRIES = {"meta.json", ".provenance.json", "versions"}

SKILL_NAME_RE = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
TOP_LEVEL_KEY_RE = re.compile(r"^([A-Za-z0-9][A-Za-z0-9_-]*):(?:[ \t]*(.*))?$")
FENCE_RE = re.compile(
    r"^[ \t]*(?:(?:[-+*]|\d+[.)])[ \t]+)?(`{3,}|~{3,})(.*)$"
)
TODO_RE = re.compile(r"^[ ]{0,3}\[TODO:[^\n]*\][ \t]*$")
MARKDOWN_LINK_RE = re.compile(
    r"(?<!!)\[[^\]]+\]\(\s*(<[^>]+>|[^)\s]+)"
)
MCP_ID_RE = re.compile(r"^[A-Za-z0-9_.-]+(?::[A-Za-z0-9_.-]+)?$")


@dataclass(frozen=True)
class Issue:
    """One actionable validator result."""

    severity: str
    code: str
    message: str
    line: Optional[int] = None

    def as_dict(self) -> Dict[str, object]:
        result: Dict[str, object] = {
            "severity": self.severity,
            "code": self.code,
            "message": self.message,
        }
        if self.line is not None:
            result["line"] = self.line
        return result


@dataclass
class ValidationReport:
    path: Path
    issues: List[Issue] = field(default_factory=list)

    @property
    def errors(self) -> List[Issue]:
        return [issue for issue in self.issues if issue.severity == "error"]

    @property
    def warnings(self) -> List[Issue]:
        return [issue for issue in self.issues if issue.severity == "warning"]

    def is_valid(self, strict: bool = False) -> bool:
        return not self.errors and (not strict or not self.warnings)

    def as_dict(self, strict: bool = False) -> Dict[str, object]:
        return {
            "path": str(self.path),
            "valid": self.is_valid(strict),
            "errors": [issue.as_dict() for issue in self.errors],
            "warnings": [issue.as_dict() for issue in self.warnings],
        }


@dataclass
class ParsedFrontMatter:
    values: Dict[str, object]
    raw_values: Dict[str, str]
    field_lines: Dict[str, int]
    lines: List[str]
    closing_line: int
    body: str
    body_start_line: int


def _strip_yaml_comment(value: str) -> str:
    """Remove a YAML comment without changing quoted ``#`` characters."""

    quote: Optional[str] = None
    escaped = False
    for index, char in enumerate(value):
        if quote == '"' and escaped:
            escaped = False
            continue
        if quote == '"' and char == "\\":
            escaped = True
            continue
        if char in {"'", '"'}:
            if quote is None:
                quote = char
            elif quote == char:
                quote = None
            continue
        if char == "#" and quote is None and (
            index == 0 or value[index - 1].isspace()
        ):
            return value[:index].rstrip()
    return value.rstrip()


def _split_inline_items(value: str) -> List[str]:
    """Split a simple YAML flow sequence while respecting quotes/brackets."""

    inner = value[1:-1].strip()
    if not inner:
        return []
    items: List[str] = []
    start = 0
    quote: Optional[str] = None
    escaped = False
    depth = 0
    for index, char in enumerate(inner):
        if quote == '"' and escaped:
            escaped = False
            continue
        if quote == '"' and char == "\\":
            escaped = True
            continue
        if char in {"'", '"'}:
            if quote is None:
                quote = char
            elif quote == char:
                quote = None
            continue
        if quote is not None:
            continue
        if char in "[{":
            depth += 1
        elif char in "]}":
            depth -= 1
        elif char == "," and depth == 0:
            items.append(inner[start:index].strip())
            start = index + 1
    items.append(inner[start:].strip())
    return items


def _parse_scalar(value: str) -> object:
    """Parse the scalar subset needed by skill frontmatter.

    NusaShell frontmatter is deliberately small: metadata and requirements
    may be nested maps, while the required fields are strings.  Full YAML
    parsing is intentionally not vendored into this portable helper.
    """

    value = _strip_yaml_comment(value).strip()
    if value == "" or value in {"~", "null", "Null", "NULL"}:
        return None
    if value.startswith("'"):
        if len(value) < 2 or not value.endswith("'"):
            raise ValueError("unterminated single-quoted value")
        return value[1:-1].replace("''", "'")
    if value.startswith('"'):
        if len(value) < 2 or not value.endswith('"'):
            raise ValueError("unterminated double-quoted value")
        # JSON string escapes are the common YAML double-quote subset.
        try:
            return json.loads(value)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid double-quoted value: {exc.msg}") from exc
    if value.startswith("["):
        if not value.endswith("]"):
            raise ValueError("unterminated flow sequence")
        return [_parse_scalar(item) for item in _split_inline_items(value)]
    if value.startswith("{"):
        if not value.endswith("}"):
            raise ValueError("unterminated flow mapping")
        # The validator only needs to know that this is a map. Nested values
        # are retained by NusaShell and are not part of this script's schema.
        return {}
    if value.lower() in {"true", "false"}:
        return value.lower() == "true"
    if re.fullmatch(r"[-+]?\d+", value):
        try:
            return int(value)
        except ValueError:
            pass
    if re.fullmatch(r"[-+]?(?:\d+\.\d*|\d*\.\d+)", value):
        try:
            return float(value)
        except ValueError:
            pass
    return value


def _parse_block_scalar(
    lines: Sequence[str], start: int, indicator: str
) -> Tuple[str, int]:
    """Read a ``|`` or ``>`` block scalar and return value plus next index."""

    block: List[str] = []
    index = start
    indents: List[int] = []
    while index < len(lines):
        line = lines[index]
        if line.strip() == "":
            block.append("")
            index += 1
            continue
        indent = len(line) - len(line.lstrip(" "))
        if indent == 0:
            break
        indents.append(indent)
        block.append(line)
        index += 1
    common_indent = min(indents) if indents else 0
    normalized = [line[common_indent:] if line else "" for line in block]
    if indicator.startswith(">"):
        value = " ".join(part.strip() for part in normalized if part.strip())
    else:
        value = "\n".join(normalized).rstrip("\n")
    return value, index


def _parse_frontmatter(raw: str) -> Tuple[Optional[ParsedFrontMatter], List[Issue]]:
    lines = raw.splitlines()
    issues: List[Issue] = []
    if not lines or lines[0].rstrip("\r") != "---":
        issues.append(
            Issue("error", "frontmatter-missing", "SKILL.md must start with '---'")
        )
        return None, issues

    closing_index: Optional[int] = None
    for index in range(1, len(lines)):
        if lines[index].rstrip("\r") == "---":
            closing_index = index
            break
    if closing_index is None:
        issues.append(
            Issue(
                "error",
                "frontmatter-unclosed",
                "frontmatter has no closing '---' delimiter",
            )
        )
        return None, issues

    front_lines = list(lines[1:closing_index])
    values: Dict[str, object] = {}
    raw_values: Dict[str, str] = {}
    field_lines: Dict[str, int] = {}
    index = 0
    while index < len(front_lines):
        line = front_lines[index]
        line_number = index + 2
        if not line.strip() or line.lstrip().startswith("#"):
            index += 1
            continue
        if line.startswith("\t"):
            issues.append(
                Issue(
                    "error",
                    "frontmatter-indent",
                    "frontmatter indentation must use spaces, not tabs",
                    line_number,
                )
            )
            index += 1
            continue
        if line[0].isspace():
            # Nested metadata/requirements lines are checked separately. An
            # indented line without a parent is still malformed YAML.
            issues.append(
                Issue(
                    "error",
                    "frontmatter-indent",
                    "frontmatter has an indented value without a top-level key",
                    line_number,
                )
            )
            index += 1
            continue

        match = TOP_LEVEL_KEY_RE.fullmatch(line)
        if not match:
            issues.append(
                Issue(
                    "error",
                    "frontmatter-syntax",
                    "frontmatter line must be a top-level 'key: value' entry",
                    line_number,
                )
            )
            index += 1
            continue

        key, raw_value = match.group(1), match.group(2) or ""
        if key in values:
            issues.append(
                Issue(
                    "error",
                    "frontmatter-duplicate",
                    f"frontmatter key '{key}' is declared more than once",
                    line_number,
                )
            )
            index += 1
            continue
        field_lines[key] = line_number
        raw_values[key] = raw_value
        try:
            if raw_value.strip().startswith(("|", ">")):
                value, next_index = _parse_block_scalar(
                    front_lines, index + 1, raw_value.strip()
                )
                index = next_index
            else:
                value = _parse_scalar(raw_value)
                index += 1
        except ValueError as exc:
            issues.append(Issue("error", "frontmatter-value", str(exc), line_number))
            value = None
            index += 1
        values[key] = value

        # Consume nested mapping/list lines. They remain available in
        # ParsedFrontMatter.lines for requirements-specific validation.
        while index < len(front_lines):
            next_line = front_lines[index]
            if next_line.strip() and not next_line[0].isspace():
                break
            index += 1

    body = "\n".join(lines[closing_index + 1 :])
    return (
        ParsedFrontMatter(
            values=values,
            raw_values=raw_values,
            field_lines=field_lines,
            lines=front_lines,
            closing_line=closing_index + 1,
            body=body,
            body_start_line=closing_index + 2,
        ),
        issues,
    )


def _add(report: ValidationReport, severity: str, code: str, message: str, line: Optional[int] = None) -> None:
    report.issues.append(Issue(severity, code, message, line))


def _validate_frontmatter(report: ValidationReport, parsed: ParsedFrontMatter) -> None:
    for key in sorted(set(parsed.values) - ALLOWED_FRONTMATTER_KEYS):
        _add(
            report,
            "error",
            "frontmatter-key",
            f"unsupported frontmatter key '{key}'; use metadata for custom fields",
            parsed.field_lines.get(key),
        )

    values = parsed.values
    name = values.get("name")
    if name is None or name == "":
        _add(report, "error", "name-missing", "frontmatter is missing a non-empty 'name'")
    elif not isinstance(name, str):
        _add(report, "error", "name-type", "frontmatter 'name' must be a string", parsed.field_lines.get("name"))
    else:
        if not SKILL_NAME_RE.fullmatch(name):
            _add(
                report,
                "error",
                "name-format",
                f"name '{name}' must be lowercase hyphen-case",
                parsed.field_lines.get("name"),
            )
        if len(name) > MAX_SKILL_NAME_LENGTH:
            _add(
                report,
                "error",
                "name-length",
                f"name is too long ({len(name)} characters); maximum is {MAX_SKILL_NAME_LENGTH}",
                parsed.field_lines.get("name"),
            )

    description = values.get("description")
    if description is None or description == "":
        _add(
            report,
            "error",
            "description-missing",
            "frontmatter is missing a non-empty 'description'",
        )
    elif not isinstance(description, str):
        _add(
            report,
            "error",
            "description-type",
            "frontmatter 'description' must be a string",
            parsed.field_lines.get("description"),
        )
    else:
        if len(description.strip()) > MAX_DESCRIPTION_LENGTH:
            _add(
                report,
                "error",
                "description-length",
                f"description is too long ({len(description.strip())} characters); maximum is {MAX_DESCRIPTION_LENGTH}",
                parsed.field_lines.get("description"),
            )
        if "<" in description or ">" in description:
            _add(
                report,
                "warning",
                "description-angle-brackets",
                "description contains angle brackets; check that they are prose rather than unfinished placeholders",
                parsed.field_lines.get("description"),
            )

    for key in ("compatibility", "license"):
        value = values.get(key)
        if value is not None and not isinstance(value, str):
            _add(
                report,
                "error",
                "frontmatter-type",
                f"frontmatter '{key}' must be a string",
                parsed.field_lines.get(key),
            )

    for key in ("metadata", "requirements"):
        value = values.get(key)
        raw_value = parsed.raw_values.get(key, "").strip()
        if value is not None and not isinstance(value, dict):
            # Empty values are the normal spelling for an indented mapping.
            _add(
                report,
                "error",
                "frontmatter-type",
                f"frontmatter '{key}' must be a mapping",
                parsed.field_lines.get(key),
            )
        elif raw_value and not raw_value.startswith("{"):
            _add(
                report,
                "error",
                "frontmatter-type",
                f"frontmatter '{key}' must be a mapping",
                parsed.field_lines.get(key),
            )

    if "allowed-tools" in values:
        _add(
            report,
            "warning",
            "runtime-field",
            "'allowed-tools' is accepted for Codex compatibility but is not enforced by NusaShell",
            parsed.field_lines.get("allowed-tools"),
        )

    _validate_mcp_requirements(report, parsed)


def _validate_mcp_requirements(report: ValidationReport, parsed: ParsedFrontMatter) -> None:
    """Validate the supported ``requirements.mcp`` list when present."""

    if "requirements" not in parsed.values:
        return

    requirements_line = parsed.field_lines.get("requirements")
    requirements_index = (requirements_line or 2) - 2
    nested: List[Tuple[int, str, int]] = []
    for index in range(requirements_index + 1, len(parsed.lines)):
        line = parsed.lines[index]
        if line.strip() and not line[0].isspace():
            break
        if line.strip():
            nested.append((len(line) - len(line.lstrip(" ")), line.strip(), index + 2))

    mcp_entry: Optional[Tuple[int, str, int]] = None
    for indent, text, line in nested:
        match = re.fullmatch(r"mcp:\s*(.*)", text)
        if match:
            mcp_entry = (indent, match.group(1), line)
            break
    if mcp_entry is None:
        return

    mcp_indent, inline_value, mcp_line = mcp_entry
    items: List[Tuple[object, int]] = []
    if inline_value.strip():
        try:
            parsed_value = _parse_scalar(inline_value)
        except ValueError as exc:
            _add(report, "error", "requirements-mcp", str(exc), mcp_line)
            return
        if not isinstance(parsed_value, list):
            _add(report, "error", "requirements-mcp", "requirements.mcp must be a list", mcp_line)
            return
        items = [(item, mcp_line) for item in parsed_value]
    else:
        for indent, text, line in nested:
            if indent > mcp_indent and text.startswith("-"):
                try:
                    items.append((_parse_scalar(text[1:].strip()), line))
                except ValueError as exc:
                    _add(report, "error", "requirements-mcp", str(exc), line)

    if not items:
        _add(report, "error", "requirements-mcp", "requirements.mcp must contain at least one item", mcp_line)
        return
    for item, line in items:
        if not isinstance(item, str) or not item.strip():
            _add(report, "error", "requirements-mcp", "each requirements.mcp item must be a non-empty string", line)
        elif not MCP_ID_RE.fullmatch(item.strip()):
            _add(
                report,
                "error",
                "requirements-mcp",
                f"requirements.mcp item '{item}' is not a plugin id or role token",
                line,
            )


def _find_unfinished_todo(body: str, start_line: int) -> Iterable[Tuple[int, str]]:
    fence_marker: Optional[str] = None
    fence_length = 0
    for offset, line in enumerate(body.splitlines()):
        fence = FENCE_RE.fullmatch(line)
        if fence:
            marker = fence.group(1)
            if fence_marker is None:
                fence_marker = marker[0]
                fence_length = len(marker)
            elif (
                marker[0] == fence_marker
                and len(marker) >= fence_length
                and not fence.group(2).strip()
            ):
                fence_marker = None
                fence_length = 0
            continue
        if fence_marker is None and TODO_RE.fullmatch(line):
            yield start_line + offset, line.strip()


def _validate_body(report: ValidationReport, parsed: ParsedFrontMatter, raw_bytes: int) -> None:
    body = parsed.body
    if not body.strip():
        _add(report, "error", "body-empty", "SKILL.md body is empty after frontmatter")
        return
    if raw_bytes > MAX_SKILL_EDITABLE_BYTES:
        _add(
            report,
            "warning",
            "skill-size",
            f"SKILL.md is {raw_bytes} bytes; NusaShell editing is limited to {MAX_SKILL_EDITABLE_BYTES} bytes",
        )
    body_lines = body.splitlines()
    if len(body_lines) > MAX_SKILL_BODY_LINES:
        _add(
            report,
            "warning",
            "body-size",
            f"SKILL.md has {len(body_lines)} body lines; keep the entrypoint near or below {MAX_SKILL_BODY_LINES} lines",
        )
    for line, text in _find_unfinished_todo(body, parsed.body_start_line):
        _add(report, "error", "todo-placeholder", f"unfinished TODO placeholder: {text}", line)

    non_empty = next((line.strip() for line in body_lines if line.strip()), "")
    if non_empty == "---":
        _add(
            report,
            "error",
            "double-frontmatter",
            "body starts with a second frontmatter delimiter; pass body-only content to skill op=save",
            parsed.body_start_line,
        )


def _walk_support_files(report: ValidationReport, skill_path: Path) -> List[Path]:
    markdown_files: List[Path] = []
    try:
        root_entries = sorted(skill_path.iterdir(), key=lambda item: item.name)
    except OSError as exc:
        _add(report, "error", "directory-read", f"cannot list skill directory: {exc}")
        return markdown_files

    for entry in root_entries:
        if entry.is_symlink():
            _add(report, "error", "symlink", f"symlinks are not allowed in skill packages: {entry.name}")
            continue
        if entry.name in ALLOWED_ROOT_FILES or entry.name in INTERNAL_ROOT_ENTRIES:
            continue
        if entry.name in ALLOWED_SUPPORT_DIRS:
            if not entry.is_dir():
                _add(report, "error", "support-root", f"{entry.name} must be a directory")
                continue
            for child in entry.rglob("*"):
                if child.is_symlink():
                    _add(report, "error", "symlink", f"symlinks are not allowed in skill packages: {child.relative_to(skill_path)}")
                elif child.is_file():
                    if child.suffix.lower() == ".md":
                        markdown_files.append(child)
                        try:
                            lines = child.read_text(encoding="utf-8").splitlines()
                        except UnicodeDecodeError:
                            _add(report, "error", "support-encoding", f"support markdown is not valid UTF-8: {child.relative_to(skill_path)}")
                            continue
                        if entry.name == "references" and len(lines) > MAX_REFERENCE_LINES:
                            _add(
                                report,
                                "warning",
                                "reference-size",
                                f"{child.relative_to(skill_path)} has {len(lines)} lines; keep references focused and below {MAX_REFERENCE_LINES} lines",
                            )
            continue
        _add(
            report,
            "warning",
            "package-entry",
            f"unsupported top-level entry '{entry.name}'; use references/, templates/, scripts/, or assets/",
        )
    return markdown_files


def _validate_links(report: ValidationReport, skill_path: Path, markdown_files: Iterable[Path]) -> None:
    root = skill_path.resolve()
    for source in [skill_path / "SKILL.md", *markdown_files]:
        try:
            text = source.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        for offset, line in enumerate(text.splitlines(), start=1):
            for match in MARKDOWN_LINK_RE.finditer(line):
                target = match.group(1).strip()
                if target.startswith("<") and target.endswith(">"):
                    target = target[1:-1]
                if not target or target.startswith(("#", "/", "//")):
                    # Root-relative links such as /docs are valid site links,
                    # not paths inside a portable skill package.
                    continue
                if re.match(r"^[A-Za-z][A-Za-z0-9+.-]*:", target):
                    continue
                target_path = target.split("#", 1)[0].split("?", 1)[0]
                if not target_path:
                    continue
                resolved = (source.parent / target_path).resolve()
                try:
                    resolved.relative_to(root)
                except ValueError:
                    _add(report, "error", "link-escape", f"markdown link escapes the skill package: {target}", offset)
                    continue
                if not resolved.exists():
                    _add(report, "error", "link-missing", f"markdown link target does not exist: {target}", offset)


def validate_skill_result(
    skill_path: Union[Path, str], check_links: bool = False
) -> ValidationReport:
    """Return structured validation results for one skill directory."""

    path = Path(skill_path)
    report = ValidationReport(path)
    if not path.exists():
        _add(report, "error", "directory-missing", f"skill directory does not exist: {path}")
        return report
    if not path.is_dir():
        _add(report, "error", "directory-type", f"skill path is not a directory: {path}")
        return report
    if path.is_symlink():
        _add(report, "error", "symlink", "skill directory must not be a symlink")
    if not SKILL_NAME_RE.fullmatch(path.name):
        _add(report, "error", "directory-name", f"directory '{path.name}' must be lowercase hyphen-case")
    if len(path.name) > MAX_SKILL_NAME_LENGTH:
        _add(report, "error", "directory-name-length", f"directory name is longer than {MAX_SKILL_NAME_LENGTH} characters")

    skill_md = path / "SKILL.md"
    if not skill_md.exists():
        _add(report, "error", "skill-md-missing", f"SKILL.md not found in {path}")
        return report
    try:
        raw_bytes = skill_md.read_bytes()
        raw = raw_bytes.decode("utf-8")
    except UnicodeDecodeError:
        _add(report, "error", "skill-md-encoding", "SKILL.md must be valid UTF-8")
        return report
    except OSError as exc:
        _add(report, "error", "skill-md-read", f"cannot read SKILL.md: {exc}")
        return report

    parsed, parse_issues = _parse_frontmatter(raw)
    report.issues.extend(parse_issues)
    if parsed is not None:
        _validate_frontmatter(report, parsed)
        name = parsed.values.get("name")
        if isinstance(name, str) and name != path.name:
            _add(
                report,
                "error",
                "name-directory-mismatch",
                f"frontmatter name '{name}' does not match directory '{path.name}'",
                parsed.field_lines.get("name"),
            )
        _validate_body(report, parsed, len(raw_bytes))

    markdown_files = _walk_support_files(report, path)
    if check_links:
        _validate_links(report, path, markdown_files)
    return report


def validate_skill(
    skill_path: Union[Path, str], strict: bool = False, check_links: bool = False
) -> Tuple[bool, str]:
    """Compatibility wrapper matching Codex's quick validator API."""

    report = validate_skill_result(skill_path, check_links=check_links)
    if report.is_valid(strict):
        return True, "Skill is valid!"
    messages = [issue.message for issue in report.errors]
    if strict:
        messages.extend(issue.message for issue in report.warnings)
    return False, "Skill validation failed: " + "; ".join(messages)


def _render_report(report: ValidationReport, strict: bool) -> str:
    lines = [f"== {report.path}"]
    for issue in report.issues:
        location = f" line {issue.line}" if issue.line is not None else ""
        lines.append(f"  [{issue.severity.upper()}] {issue.code}{location}: {issue.message}")
    status = "PASS" if report.is_valid(strict) else "FAIL"
    lines.append(
        f"  -> {status} ({len(report.errors)} errors, {len(report.warnings)} warnings)"
    )
    return "\n".join(lines)


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("paths", nargs="+", metavar="SKILL_DIR")
    parser.add_argument(
        "--strict",
        action="store_true",
        help="treat warnings as failures",
    )
    parser.add_argument(
        "--check-links",
        action="store_true",
        help="verify relative Markdown links inside the package",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="emit machine-readable reports instead of text",
    )
    args = parser.parse_args(argv)

    reports = [
        validate_skill_result(path, check_links=args.check_links) for path in args.paths
    ]
    if args.json:
        print(json.dumps([report.as_dict(args.strict) for report in reports], indent=2))
    else:
        print("\n\n".join(_render_report(report, args.strict) for report in reports))
    return 0 if all(report.is_valid(args.strict) for report in reports) else 1


if __name__ == "__main__":
    sys.exit(main())
