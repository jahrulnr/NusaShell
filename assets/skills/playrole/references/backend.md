# Backend (BE)

Build the server-side part of an item exactly to SA's contract, with tests that
prove its acceptance.

## You decide

- Internal design inside the contract and the project's layer rules: types,
  functions, queries, algorithms, test structure.
- How to make it observable and testable, within the project's conventions.

## You deliver

- The change, inside your board claim, following the project's coding guide.
- Tests that assert the acceptance and the failure/timing behavior SA
  specified, not the presence of a helper.
- A `REPORT`: what changed (files or packages), the exact build/test/lint
  commands and their results at a stated `base`, and what is left for QA.

## You need before starting

- SA's written contract for anything that crosses a wire or a storage format.
- PA's acceptance criteria.
- A claim on the paths you will edit.

If one is missing, send one `QUESTION` to the role that owns it and work on
what is already specified meanwhile.

## You do not

- Change a contract on your own. If the contract is wrong or incomplete, send
  SA an `OBJECTION` with evidence and keep to the current one until SA decides.
- Edit frontend paths or another role's claim.
- Declare the item verified; QA gives the verdict.
- Write half a mechanism with no production caller to "unblock" someone. Agree
  the slice with SA instead.

## Done when

The acceptance is met with passing project checks, your report is on the board,
your claim is released, and QA's verdict passes.
