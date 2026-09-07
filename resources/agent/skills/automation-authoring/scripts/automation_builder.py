#!/usr/bin/env python3
"""Render a disabled NusaShell automation template.

This helper is intentionally stdlib-only and performs no dispatcher call. It
copies one bundled template, replaces only its top-level ``name`` field, and
writes atomically. Always run the rendered YAML through automation(op="validate")
before creating or enabling it.

Examples:
  python3 automation_builder.py list
  python3 automation_builder.py check
  python3 automation_builder.py new --template simple-shell-check.yaml \
      --name "Workspace check" --output /tmp/workspace-check.yaml
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile
from pathlib import Path

TEMPLATES = Path(__file__).resolve().parents[1] / "templates"
TOP_LEVEL_NAME = re.compile(r"^name:\s*.*(?:\r?\n)?$")
TOP_LEVEL_ENABLED_TRUE = re.compile(r"^enabled:\s*(?:true|yes|on)\s*(?:#.*)?$", re.I)
REQUIRED_MARKERS = ("version:", "name:", "enabled:", "triggers:", "jobs:")


def template_path(filename: str) -> Path:
    """Resolve a template basename without allowing path traversal."""
    candidate = Path(filename)
    if candidate.name != filename or candidate.suffix not in {".yaml", ".yml"}:
        raise ValueError("template must be a YAML basename inside templates/")
    path = (TEMPLATES / candidate.name).resolve()
    if path.parent != TEMPLATES.resolve() or not path.is_file():
        raise FileNotFoundError(f"template not found: {filename}")
    return path


def list_templates() -> int:
    for path in sorted(TEMPLATES.glob("*.yaml")):
        print(path.name)
    return 0


def replace_top_level_name(text: str, name: str) -> str:
    encoded = json.dumps(name, ensure_ascii=False)
    lines = text.splitlines(keepends=True)
    for index, line in enumerate(lines):
        if TOP_LEVEL_NAME.match(line) and not line.startswith((" ", "\t")):
            ending = "\r\n" if line.endswith("\r\n") else "\n" if line.endswith("\n") else ""
            lines[index] = f"name: {encoded}{ending}"
            return "".join(lines)
    raise ValueError("template has no top-level name field")


def atomic_write(path: Path, content: str, force: bool) -> None:
    path = path.expanduser().resolve()
    if path.exists() and not force:
        raise FileExistsError(f"output exists (use --force to replace): {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp_name, path)
    except Exception:
        try:
            os.unlink(temp_name)
        except FileNotFoundError:
            pass
        raise


def check_templates() -> int:
    errors: list[str] = []
    paths = sorted(TEMPLATES.glob("*.yaml"))
    if not paths:
        errors.append("no .yaml templates found")
    for path in paths:
        text = path.read_text(encoding="utf-8")
        for marker in REQUIRED_MARKERS:
            if not any(line.startswith(marker) for line in text.splitlines()):
                errors.append(f"{path.name}: missing top-level marker {marker}")
        if any(TOP_LEVEL_ENABLED_TRUE.match(line) for line in text.splitlines()):
            errors.append(f"{path.name}: templates must not be enabled by default")
        if "REPLACE_ME" not in text and "replace" not in text.lower():
            # This is advisory only. Executable examples are allowed.
            print(f"warning: {path.name} has no obvious replacement marker", file=sys.stderr)
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1
    print(f"checked {len(paths)} disabled template(s); dispatcher validation still required")
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", help="list template basenames")
    sub.add_parser("check", help="run conservative template safety checks")
    new = sub.add_parser("new", help="copy one template and replace its name")
    new.add_argument("--template", required=True, help="template basename")
    new.add_argument("--name", required=True, help="workflow name")
    new.add_argument("--output", required=True, type=Path, help="destination YAML path")
    new.add_argument("--force", action="store_true", help="replace an existing destination")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.command == "list":
            return list_templates()
        if args.command == "check":
            return check_templates()
        source = template_path(args.template)
        content = replace_top_level_name(source.read_text(encoding="utf-8"), args.name)
        atomic_write(args.output, content, args.force)
        print(args.output.expanduser().resolve())
        print("next: call automation(op=\"validate\", yaml=<file contents>)")
        return 0
    except (OSError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
