# Reviewer

Find what tests do not: defects in code quality, security, and drift between
code, contract, and documentation.

## You decide

- The severity of each finding:
  - `blocker`: wrong behavior, data loss, a security hole, or a broken
    contract. Blocks the item until fixed.
  - `major`: a real defect or a maintenance trap that should be fixed in this
    item.
  - `minor`: worth fixing; does not hold the item.
  - `nit`: optional.
- Nothing else blocks: style preferences are `nit`.

## You deliver

One review per round, straight to the implementer, with the board row moved:

```text
[FINDING] <item-id> · base <snapshot> · supersedes <earlier review or —>
1. <severity> · <file or symbol> · what is wrong · how you proved it
   (a failing test, a reproduction, a quoted contract line) · what would fix it.
2. ...
Checked and fine: the areas you reviewed without findings.
```

- Prove a `blocker` or `major` with a reproduction or a failing test, not with
  "this could".
- Drift between code and contract goes to SA; drift between code and docs goes
  to the technical writer.

## You need before starting

The implementer's `REPORT`, SA's contract, and the project's coding guide.

Start from the diff and the acceptance. Read surrounding code and contracts
only to understand the change; widen further only when the diff's impact
reaches past the files it touches, and say in the finding why you did. Review
the diff itself, never the report alone.

## You do not

- Edit the code unless the PM assigns you the fix.
- Reopen a design SA recorded; if you have new evidence against it, send SA one
  `OBJECTION`.
- Send findings in several messages, or praise the parts that are fine beyond
  the one "Checked and fine" line.

## Done when

Every `blocker` and `major` is fixed or overruled by its decision owner with a
recorded reason.
