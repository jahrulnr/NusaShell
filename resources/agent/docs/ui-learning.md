# Learning

Search accumulated memory entries and skills via hybrid BM25 + embedding + graph search, explore how they connect in the knowledge graph, and review what autolearn saved.

**How to open:** Click the Learning item in the left sidebar.

## Header stats

Live counts of memory entries and learning edges. The memory count refreshes in real time when a tool or the background review agent saves, promotes, or demotes a memory entry (via the `memory.updated` event).

- **Memory count** (`#learning-stat-memory`):
  - Section: Learning
  - Type: text

- **Edge count** (`#learning-stat-edges`):
  - Section: Learning
  - Type: text

## Tabs

Switches between Memory & Graph (search + graph) and Learning log (autolearn activity feed). The log loads lazily on first open.

- **Memory & Graph tab** (`#learning-tab-memory`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the search + knowledge graph panel.

- **Learning log tab** (`#learning-tab-log`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the autolearn activity feed.

## Search bar

Hybrid search across memory and skills, with a kind filter (All, Skills, Memory) and a Search button. On phones the query and filter/action controls use separate full-width rows so none of the controls are clipped.

- **Learning search** (`#learning-search-input`):
  - Section: Learning
  - Type: search

- **Kind filter** (`#learning-kind-filter`):
  - Section: Learning
  - Type: select
  - Notes: All, Skills, Memory.

- **Search button** (`#learning-search-btn`):
  - Section: Learning
  - Type: button
  - Action: Runs the hybrid search.

## Results

Ranked search results with a count. With an empty query the pane lists all skills and memories so content is visible immediately. Memory entries show a tier badge (primary or fragment) next to the kind label so the two tiers are visually distinguishable. Memory entries have a delete button (×) that opens a confirm dialog and calls memory.delete. Long content is collapsed to 3 lines; click to expand. Results refresh automatically when memory or skills change (via `memory.updated` / `skill.updated` events, debounced 300ms).

- **Results** (`#learning-results`):
  - Section: Learning
  - Type: list

- **Results count** (`#learning-results-count`):
  - Section: Learning
  - Type: text

## Splitter

Draggable splitter between the results pane and the graph pane. Drag to resize the results pane width; the width persists to localStorage.

- **`#learning-splitter`** (missing map entry)

## Knowledge graph

Force-directed graph (vis-network) of skills, memory entries, and their edges. Related edges are rebuilt from content similarity plus specific fragment metadata (project, task, and non-ubiquitous tags); stale edges to deleted nodes are removed. Used-with edges connect learning nodes observed together during one successful agent/review turn. After its bounded physics pass, the graph resolves residual collisions so node circles retain a visible gap before the layout freezes. Reload fetches the graph and performs a full relayout; Fit fits it to view. Legend distinguishes Skill, Memory, Related, and Used-with edges. The graph refreshes automatically when memory or skills change or a review finishes (via `memory.updated` / `skill.updated` / `learning.review.done` events, coalesced with a 300ms debounce); automatic refreshes keep existing node positions and lay out only additions, so the graph stays still while idle. On narrow screens results and graph stack into separate bounded panes with their own scrollable result list.

- **Knowledge graph** (`#learning-graph`):
  - Section: Learning
  - Type: visualization
  - Notes: vis-network force-directed graph.

- **Reload graph** (`#learning-graph-refresh`):
  - Section: Learning
  - Type: button
  - Action: Reloads the graph from the backend and performs a full relayout with node spacing.

- **Fit to view** (`#learning-graph-fit`):
  - Section: Learning
  - Type: button
  - Action: Fits the graph to the viewport.

## Learning log

Autolearn activity feed from the trajectory log (review runs, extraction, edge building, consolidation, decay, prune), newest first. Review entries show a status badge (done or error) with the error message when failed, a compact Source <title> line (raw conversation id hidden), the saved outcomes (kind + snippet) or an explicit Nothing to save. line, per-type extras, and a View review details button. Expanding it replays what the background review agent did exactly like an Agent-view conversation - Thinking disclosures per round, terminal-style tool cards with input/output panels, short narration notes, and the final verdict - but never the replayed user transcript. A running indicator appears at the top while a review is in-flight. Refresh reloads the feed.

- **Learning log count** (`#learning-log-count`):
  - Section: Learning
  - Type: text

- **Refresh learning log** (`#learning-log-refresh`):
  - Section: Learning
  - Type: button
  - Action: Reloads the autolearn trajectory feed.

- **Learning log** (`#learning-log`):
  - Section: Learning
  - Type: list
  - Notes: Trajectory events; review entries have done/error/skipped status badges. Skipped entries explain whether a trigger was coalesced because another review was running or deferred by retry cooldown. Completed review entries have a View review details button that expands the background review agent's activity digest inline.
