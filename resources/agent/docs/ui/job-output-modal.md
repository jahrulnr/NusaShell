# Job output modal

Shows recent run output entries (run time, status, summary) for a selected job.

**How to open:** Click the Output button on a job row.

## Output entries

Lists up to 20 recent output entries newest-first, each showing the run timestamp, status (ok/error/cancelled), and a truncated summary. Each entry has a Show full output button that loads the full markdown body from disk via job.output with includeBody. The dialog supports Escape and overlay click to close.

- **Output entries** (`#job-output-body`):
  - Section: Output entries
  - Type: list
  - Action: Renders recent run output entries.

- **Show full output** (`[data-control="job-output-expand"]`):
  - Section: Output entries
  - Type: button
  - Action: Loads the full markdown body for a run entry via job.output with includeBody.
