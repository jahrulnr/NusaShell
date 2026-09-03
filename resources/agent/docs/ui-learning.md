# Learning

Edit the always-injected user.md and soul.md documents, search accumulated memory entries and skills via hybrid BM25 + embedding + graph search, explore how they connect in the knowledge graph, and review what autolearn saved.

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

Opens About You first, where the always-injected user.md document can be edited and saved. About Agent provides the matching editor for the agent working-knowledge document. Learning uses the same compact segmented tab control as Automation, kept apart from the content panel so navigation does not appear attached to a card edge. Memory & Graph provides search + graph, and Learning log shows autolearn activity; the log loads lazily on first open.

- **About You tab** (`#learning-tab-about`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the editable always-injected user memory document.

- **About Agent tab** (`#learning-tab-agent`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the editable always-injected soul.md document.

- **Memory & Graph tab** (`#learning-tab-memory`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the search + knowledge graph panel.

- **Learning log tab** (`#learning-tab-log`):
  - Section: Learning
  - Type: tab
  - Notes: Shows the autolearn activity feed.

## About You

The editable user memory document. Reload reads the persisted value again; Save changes replaces the complete user memory document through memory.user.update. The editor shows the 4000-character cap and allows an intentional empty document. The card expands across the available desktop canvas while keeping a compact inset and full-width editor on mobile.

- **User memory status** (`#learning-user-status`):
  - Section: Learning
  - Type: status
  - Notes: Reports whether user memory is loaded, empty, saved, or unavailable.

- **User memory editor** (`#learning-user-memory`):
  - Section: Learning
  - Type: textarea
  - Action: Edits the complete user memory document.

- **User memory character count** (`#learning-user-count`):
  - Section: Learning
  - Type: text

- **Reload user memory** (`#learning-user-reload`):
  - Section: Learning
  - Type: button
  - Action: Discards the current draft and reloads the persisted user memory document.

- **Save user memory** (`#learning-user-save`):
  - Section: Learning
  - Type: button
  - Action: Replaces the persisted user memory document.

## Soul.md

The editable agent-tier memory document. Reload reads the persisted value again; Save changes replaces the complete soul.md document through memory.agent.update. The editor shows the 4000-character cap and allows an intentional empty document. Changes remain separate from user.md and fragment memory.

- **Soul memory status** (`#learning-agent-status`):
  - Section: Learning
  - Type: status
  - Notes: Reports whether soul.md is loaded, empty, saved, or unavailable.

- **Soul memory editor** (`#learning-agent-memory`):
  - Section: Learning
  - Type: textarea
  - Action: Edits the complete soul.md document.

- **Soul memory character count** (`#learning-agent-count`):
  - Section: Learning
  - Type: text

- **Reload soul memory** (`#learning-agent-reload`):
  - Section: Learning
  - Type: button
  - Action: Discards the current draft and reloads the persisted soul.md document.

- **Save soul memory** (`#learning-agent-save`):
  - Section: Learning
  - Type: button
  - Action: Replaces the persisted soul.md document.

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

Ranked search results with a count. With an empty query the pane lists all skills and memories so content is visible immediately. Memory entries show a tier badge (user or fragment) next to the kind label so the two tiers are visually distinguishable. Memory entries have a delete button (×) that opens a confirm dialog and calls memory.delete. Long content is collapsed to 3 lines; click to expand. Results refresh automatically when memory or skills change (via `memory.updated` / `skill.updated` events, debounced 300ms).

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

Force-directed graph (vis-network) of skills, memory entries, and their edges. Related edges are rebuilt from content similarity plus specific fragment metadata (project, task, and non-ubiquitous tags); stale edges to deleted nodes are removed. Used-with edges connect learning nodes observed together during one successful agent/review turn. Dense edge lines stay thin. Node size scales with the number of unique neighbouring nodes, making the most-connected hubs largest; hover text includes the relation count. Zooming out preserves most of that size contrast so nearby relation counts remain visually distinct instead of collapsing to the same rasterized radius. After its bounded physics pass, a full layout keeps the force-directed angular grouping but places highly connected hubs toward the center and low-degree or isolated nodes toward a compact perimeter ring without detaching them from the main cluster, then resolves residual collisions before freezing. The restrained archipelago palette maps skills to ocean blue, fragments to earth brown, user memory to leaf green, and edges to deeper ocean, mangrove, or sand tones. Reload fetches the graph and performs this full relayout; Fit fits it to view. Legend distinguishes Skill, Memory, Related, and Used-with edges. The graph refreshes automatically when memory or skills change or a review finishes (via `memory.updated` / `skill.updated` / `learning.review.done` events, coalesced with a 300ms debounce); automatic refreshes keep existing node positions and lay out only additions, so the graph stays still while idle. On narrow screens results and graph stack into separate bounded panes with their own scrollable result list.

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

Autolearn activity feed from the trajectory log (review runs, extraction, edge building, consolidation, decay, prune), newest first. Review entries show a status badge (done or error); failures use a concise automatic-processing message rather than exposing a verbose provider body. Each entry has a compact Source <title> line (raw conversation id hidden), the saved outcomes (kind + snippet) or an explicit Nothing to save. line, per-type extras, and a View review details button. Expanding it replays what the background review agent did exactly like an Agent-view conversation - Thinking disclosures per round, terminal-style tool cards with input/output panels, short narration notes, and the final verdict - but never the replayed user transcript. A running indicator appears at the top while a review is in-flight. Refresh reloads the feed. There is no manual retry action for background reviews.

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
  - Notes: Trajectory events; review entries have done/error/skipped status badges. Failed entries show a concise automatic-processing message; raw provider diagnostics stay server-side. Skipped entries explain whether a trigger was coalesced because another review was running or deferred by retry cooldown. Review entries have a View review details button that expands the background review agent's activity digest inline; there is no manual retry button.
