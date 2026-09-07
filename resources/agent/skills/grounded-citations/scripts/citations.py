#!/usr/bin/env python3
"""Citation ledger for grounded-citations skill. Stdlib only.

Usage:
  citations.py reset
  citations.py add <url> [--title T] [<url2> ...]
  citations.py quit 1 --text "..." --from page.txt
  citations.py list [--json]
  citations.py render [--style markdown|plain|evidence] [--cited-in draft.md | --replace-in draft.md | --only 1,3]
  citations.py verify draft.md [--strict] [--min-coverage 0.6] [--evidence]
"""
import argparse
import json
import re
import sys
from pathlib import Path

LEDGER_DEFAULT = Path(".citations/ledger.json")
LOCK = object()  # noqa


def ledger_path():
    import os
    p = os.environ.get("CITATION_LEDGER")
    return Path(p) if p else LEDGER_DEFAULT


def load():
    p = ledger_path()
    if not p.exists():
        return {"sources": []}
    return json.loads(p.read_text())


def save(data):
    p = ledger_path()
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_text(json.dumps(data, indent=2, ensure_ascii=False))


def norm_url(url):
    url = url.strip().rstrip("/")
    return url


def find_id(data, url):
    for s in data["sources"]:
        if s["url"] == norm_url(url):
            return s["id"]
    return None


def cmd_reset(_):
    save({"sources": []})
    print("ledger reset")


def cmd_add(args):
    data = load()
    for url in args.urls:
        u = norm_url(url)
        if find_id(data, u) is None:
            data["sources"].append({
                "id": len(data["sources"]) + 1,
                "url": u,
                "title": args.title or "",
                "quotes": [],
            })
            save(data)
        print(find_id(data, u))


def cmd_quote(args):
    data = load()
    s = next((x for x in data["sources"] if x["id"] == args.id), None)
    if s is None:
        sys.exit(f"no source with id {args.id}")
    evidence = Path(args.from_file)
    if not evidence.exists():
        sys.exit(f"evidence file not found: {evidence}")

    def norm(t):
        # strip markdown links/emphasis, collapse whitespace, lowercase
        t = re.sub(r"\[([^\]]*)\]\([^)]*\)", r"\1", t)
        t = re.sub(r"[*_`#>\-]", " ", t)
        return re.sub(r"\s+", " ", t).strip().lower()

    ev = norm(evidence.read_text())
    q = norm(args.text)
    if q and q not in ev:
        sys.exit(f"quote not found verbatim in {evidence}")
    s["quotes"].append({"text": args.text, "from": str(evidence)})
    save(data)
    print(f"quote attached to [{s['id']}]")


def cmd_list(args):
    data = load()
    for s in data["sources"]:
        q = f" ({len(s['quotes'])} quotes)" if s["quotes"] else ""
        print(f"[{s['id']}] {s['url']}  {s.get('title','')}{q}")


def cmd_render(args):
    data = load()
    ids = None
    if args.cited_in or args.replace_in:
        text = Path(args.cited_in or args.replace_in).read_text()
        ids = sorted({int(m) for m in re.findall(r"\[(\d+)\]", text)})
    elif args.only:
        ids = sorted({int(x) for x in args.only.split(",")})
    sources = [s for s in data["sources"] if ids is None or s["id"] in ids]
    style = args.style
    if style == "evidence":
        lines = ["## Sources"]
        for s in sources:
            lines.append(f"- {s['url']}")
            for q in s["quotes"]:
                lines.append(f"  > {q['text']}")
        return "\n".join(lines)
    if style == "plain":
        return "Sources: " + "; ".join(f"[{s['id']}] {s['url']}" for s in sources)
    lines = ["## Sources"]
    for s in sources:
        lines.append(f"- [{s['id']}] {s['url']}")
    out = "\n".join(lines)
    if args.replace_in:
        p = Path(args.replace_in)
        text = p.read_text()
        text = re.sub(r"## Sources\n.*", out, text, flags=re.S)
        p.write_text(text)
        print(out)
    else:
        print(out)


def cmd_verify(args):
    p = Path(args.draft)
    if not p.exists():
        sys.exit(f"draft not found: {p}")
    data = load()
    text = p.read_text()
    # strip Sources block and non-prose lines for coverage
    body = re.split(r"^## Sources\b", text, flags=re.M)[0]
    prose = [ln for ln in body.splitlines()
             if len(re.sub(r"[^\w]", " ", ln).split()) >= 4
             and not ln.lstrip().startswith(("#", "|"))
             and not ln.strip().startswith("```")]
    known = {s["id"] for s in data["sources"]}
    cited = {int(m) for m in re.findall(r"\[(\d+)\]", text)}
    unknown = cited - known
    ok = True
    if unknown:
        ok = False
        print(f"ERROR: unknown citation ids: {sorted(unknown)}")
    if args.evidence:
        for s in data["sources"]:
            if not s.get("quotes") and s["id"] in cited:
                ok = False
                print(f"ERROR: source [{s['id']}] cited but has no evidence quote")
    if args.min_coverage is not None:
        covered = sum(1 for ln in prose if re.search(r"\[\d+\]|\[unverified\]", ln))
        ratio = covered / len(prose) if prose else 0.0
        print(f"info: coverage {ratio:.0%} ({covered}/{len(prose)} sentences)")
        if ratio < args.min_coverage:
            ok = False
    print(f"info: sources={len(data['sources'])} cited={sorted(cited)}")
    sys.exit(0 if ok else 1)


def main():
    ap = argparse.ArgumentParser(description="citation ledger")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sub.add_parser("reset")
    a = sub.add_parser("add")
    a.add_argument("urls", nargs="+")
    a.add_argument("--title", default="")
    a.add_argument("--ingest", help="JSON array of {url,title} from tool output")
    q = sub.add_parser("quote")
    q.add_argument("id", type=int)
    q.add_argument("--text", required=True)
    q.add_argument("--from", dest="from_file", required=True)
    l = sub.add_parser("list")
    l.add_argument("--json", action="store_true")
    r = sub.add_parser("render")
    r.add_argument("--style", choices=["markdown", "plain", "evidence"], default="markdown")
    r.add_argument("--cited-in")
    r.add_argument("--replace-in")
    r.add_argument("--only")
    v = sub.add_parser("verify")
    v.add_argument("draft")
    v.add_argument("--strict", action="store_true")
    v.add_argument("--min-coverage", type=float)
    v.add_argument("--evidence", action="store_true")

    args = ap.parse_args()
    if args.cmd == "reset":
        cmd_reset(args)
    elif args.cmd == "add":
        cmd_add(args)
    elif args.cmd == "quote":
        cmd_quote(args)
    elif args.cmd == "list":
        cmd_list(args)
    elif args.cmd == "render":
        cmd_render(args)
    elif args.cmd == "verify":
        cmd_verify(args)


if __name__ == "__main__":
    main()