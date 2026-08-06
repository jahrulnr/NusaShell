# Pipelines workspace

The Pipelines view is the shell's orchestration control plane. It should feel
like an execution instrument rather than a generic CRUD list: graphite surfaces,
thin signal lines, monospace operational labels, and phosphor-lime state/action
accents keep it coherent with the NusaShell workbench.

## Visual contract

- The header names the orchestration role and keeps the primary create action on
  the right edge.
- Each pipeline is an execution rail: a narrow state signal, identity and
  description, a compact left-to-right step flow, then trigger/state metadata
  and a small action hierarchy.
- Inspect and Run now are the visible actions. Edit, pause/resume, and delete
  belong in the overflow menu so destructive and secondary actions do not
  compete with the run path.
- Empty state uses a small connected-node motif and a direct create action.
- On narrow windows, the card becomes a stacked rail and the step flow remains
  horizontally scrollable rather than collapsing labels into unreadable rows.
- The Details modal keeps the complete DAG as the primary frame in every state;
  run output is a secondary, per-step Markdown panel below it. A completed run
  must never replace the pipeline shape with a single output/job card.

The signature element is the vertical signal rail: it makes pipeline health
readable before the user parses any copy and reserves lime for live/ready state.
