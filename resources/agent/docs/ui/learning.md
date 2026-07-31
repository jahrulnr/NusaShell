# Learning

Visualizes the agent's learning journey as a timeline and constellation of skill and memory nodes, with a time scrubber and a detail pane for inspecting and editing individual nodes.

**How to open:** Click the Learning item in the left sidebar.

## Stats

Summary cards showing the total number of learned skills, memory entries, agent-created nodes, and nodes used in turns.

- **Learned skills count** (`#learning-stat-skills`):
  - Section: Stats
  - Type: stat card
  - Action: Shows the total number of learned skill nodes in the graph.

- **Memory entries count** (`#learning-stat-memory`):
  - Section: Stats
  - Type: stat card
  - Action: Shows the total number of memory entry nodes in the graph.

- **Agent-created count** (`#learning-stat-agent`):
  - Section: Stats
  - Type: stat card
  - Action: Shows the number of nodes created by the agent (as opposed to user-created).

- **Used in turns count** (`#learning-stat-used`):
  - Section: Stats
  - Type: stat card
  - Action: Shows the number of nodes that have been used in at least one agent turn.

## Timeline

A chronological list of learning nodes grouped by day. Each row shows a colored dot indicating kind (skill or memory) and state (active, stale, archived), the node label, category, use count, and a pin indicator. Click a row to inspect it in the detail pane.

- **Learning timeline** (`#learning-timeline-list`):
  - Section: Timeline
  - Type: listbox
  - Action: Chronological list of learning nodes grouped by day. Click a row to inspect it in the detail pane.

- **Timeline node count** (`#learning-timeline-count`):
  - Section: Timeline
  - Type: status text
  - Action: Shows the total number of nodes in the timeline.

- **Learning empty state** (`#learning-empty`):
  - Section: Timeline
  - Type: status
  - Action: Shown when the agent has not created any skills or memory entries yet.

## Constellation

An SVG graph that positions nodes by timestamp (horizontal) and cluster category (vertical) and draws edges between related skills. Node color reflects kind and state. Click a dot to select the node. A time scrubber below fades nodes that fall after the selected percentage of the timeline.

- **Constellation graph** (`#learning-constellation-svg`):
  - Section: Constellation
  - Type: svg graph
  - Action: Visualizes learning nodes as dots positioned by timestamp and cluster, with edges between related skills. Click a dot to select a node.

- **Constellation metadata** (`#learning-constellation-meta`):
  - Section: Constellation
  - Type: status text
  - Action: Shows the number of edges in the constellation graph.

- **Time scrubber** (`#learning-scrubber`):
  - Section: Constellation
  - Type: range slider
  - Action: Fades nodes that fall after the selected percentage of the timeline, revealing how the journey grew over time.

- **Scrubber value** (`#learning-scrubber-value`):
  - Section: Constellation
  - Type: status text
  - Action: Shows the current scrubber percentage.

## Detail

Shows the selected node's label and content. Memory nodes can be edited and saved; skill nodes are read-only and can be archived. Delete archives skills or removes memory entries after confirmation.

- **Node detail metadata** (`#learning-detail-meta`):
  - Section: Detail
  - Type: status text
  - Action: Shows the label of the currently selected node.

- **Detail empty state** (`#learning-detail-empty`):
  - Section: Detail
  - Type: status
  - Action: Prompt to select a node from the timeline or constellation.

- **Node content editor** (`#learning-detail-editor`):
  - Section: Detail
  - Type: textarea
  - Action: Shows the content of the selected node. Memory nodes can be edited; skill nodes are read-only.

- **Save node** (`#learning-save-btn`):
  - Section: Detail
  - Type: button
  - Action: Saves edits to the selected memory node's content.

- **Delete / Archive node** (`#learning-delete-btn`):
  - Section: Detail
  - Type: button
  - Action: Archives the selected skill node or removes the selected memory entry after confirmation.

- **Refresh graph** (`#learning-refresh-btn`):
  - Section: Detail
  - Type: button
  - Action: Reloads the entire learning graph from the backend.
