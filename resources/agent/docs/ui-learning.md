# Learning

Edit the always-injected user.md and soul.md documents, browse recorded experience and structured memory records, search records and skills via hybrid BM25 + embedding + graph search, explore how they connect in the knowledge graph, and review what background learning jobs saved.

**How to open:** Click the Learning item in the left sidebar.

## Header stats

Live counts of memory records and learning edges. The record count refreshes in real time when a learner job, a human retire, or an About You/About Agent save changes memory.

- **Memory count** (`#learning-stat-memory`):
  - Type: text

- **Edge count** (`#learning-stat-edges`):
  - Type: text

## Tabs

Opens About You first, where the always-injected user.md document can be edited and saved. About Agent provides the matching editor for the agent working-knowledge document. Learning uses the same compact segmented tab control as Automation, kept apart from the content panel so navigation does not appear attached to a card edge. Experience lists recorded episodes and structured memory records. Memory & Graph provides search + graph, and Learning log shows learning-job activity; the log loads lazily on first open.

- **About You tab** (`#learning-tab-about`):
  - Type: tab
  - Notes: Shows the editable always-injected user memory document.

- **About Agent tab** (`#learning-tab-agent`):
  - Type: tab
  - Notes: Shows the editable always-injected soul.md document.

- **Experience tab** (`#learning-tab-experience`):
  - Type: tab
  - Notes: Shows recorded episodes and structured memory records.

- **Memory & Graph tab** (`#learning-tab-memory`):
  - Type: tab
  - Notes: Shows the search + knowledge graph panel.

- **Learning log tab** (`#learning-tab-log`):
  - Type: tab
  - Notes: Shows the learning-job activity feed.

## About You

The editable user memory document. Reload reads the persisted value again; Save changes replaces the complete persisted user memory document. The editor shows the 4000-character cap and allows an intentional empty document. The card expands across the available desktop canvas while keeping a compact inset and full-width editor on mobile.

- **User memory status** (`#learning-user-status`):
  - Type: status
  - Notes: Reports whether user memory is loaded, empty, saved, or unavailable.

- **User memory editor** (`#learning-user-memory`):
  - Type: textarea
  - Action: Edits the complete user memory document.

- **User memory character count** (`#learning-user-count`):
  - Type: text

- **Reload user memory** (`#learning-user-reload`):
  - Type: button
  - Action: Discards the current draft and reloads the persisted user memory document.

- **Save user memory** (`#learning-user-save`):
  - Type: button
  - Action: Replaces the persisted user memory document.

## Soul.md

The editable agent-tier memory document. Reload reads the persisted value again; Save changes replaces the complete persisted soul.md document. The editor shows the 4000-character cap and allows an intentional empty document. Changes remain separate from user.md and memory records.

- **Soul memory status** (`#learning-agent-status`):
  - Type: status
  - Notes: Reports whether soul.md is loaded, empty, saved, or unavailable.

- **Soul memory editor** (`#learning-agent-memory`):
  - Type: textarea
  - Action: Edits the complete soul.md document.

- **Soul memory character count** (`#learning-agent-count`):
  - Type: text

- **Reload soul memory** (`#learning-agent-reload`):
  - Type: button
  - Action: Discards the current draft and reloads the persisted soul.md document.

- **Save soul memory** (`#learning-agent-save`):
  - Type: button
  - Action: Replaces the persisted soul.md document.

## Experience

Paginated list of recorded episodes (newest first, 25 per page, Prev/Next controls). Each row shows the goal, outcome status, signals, and timestamp, with a per-row delete control behind a styled confirm. Clicking a row expands detail. Structured memory records sit beside it; a selected record can be deleted behind a styled confirm. The lists refresh automatically when experience, memory, or learning jobs change.

- **Experience list** (`#learning-experience-list`):
  - Type: list
  - Notes: Read-only episodes, newest first. Click a row to expand detail.

- **Memory records list** (`#learning-records-list`):
  - Type: list
  - Notes: Structured memory records (tier record). Select a row, then delete.

- **Delete memory record** (`#learning-record-delete`):
  - Type: button
  - Action: Deletes the record after a styled confirm. Removes the record, its graph edges, and its retrieval presence permanently. Disabled until a record is selected.

## Search bar

Hybrid search across memory and skills, with a kind filter (All, Skills, Memory) and a Search button. On phones the query and filter/action controls use separate full-width rows so none of the controls are clipped.

- **Learning search** (`#learning-search-input`):
  - Type: search

- **Kind filter** (`#learning-kind-filter`):
  - Type: select
  - Notes: All, Skills, Memory.

- **Search button** (`#learning-search-btn`):
  - Type: button
  - Action: Runs the hybrid search.

## Results

Ranked search results with a count. With an empty query the pane lists all skills and records so content is visible immediately. Memory hits show a tier badge (user, agent, or record) next to the kind label. Records are retired from the Experience tab, not deleted here. Long content is collapsed to 3 lines; click to expand. Results refresh automatically when memory or skills change.

- **Results** (`#learning-results`):
  - Type: list

- **Results count** (`#learning-results-count`):
  - Type: text

## Splitter

Draggable splitter between the results pane and the graph pane. Drag to resize the results pane width; the width persists in this browser.

- **Results/graph splitter** (`#learning-splitter`):
  - Type: dragger
  - Action: Drag horizontally to resize the search results pane; the graph pane takes the remaining width.
  - Notes: Persists the results-pane width in this browser; inert below 900px where results and graph stack vertically.

## Knowledge graph

Force-directed graph of skills, memory records, and their edges. Related edges are rebuilt from content similarity plus record metadata; stale edges to deleted nodes are removed. Used-with edges connect learning nodes observed together during one successful agent or learning-job turn. The backend coalesces unchanged graph rebuilds and emits a refresh event when newly derived edges are ready. Dense edge lines stay thin. Node size scales with the number of unique neighbouring nodes, making the most-connected hubs largest; hover text includes the relation count. Zooming out preserves most of that size contrast so nearby relation counts remain visually distinct instead of collapsing to the same rasterized radius. After its bounded physics pass, a full layout keeps the force-directed angular grouping but places highly connected hubs toward the center and low-degree or isolated nodes toward a compact perimeter ring without detaching them from the main cluster, then resolves residual collisions before freezing. The palette maps skills to blue, records to earth brown, user memory to leaf green, and edges to deeper blue, green, or sand tones. Reload fetches the graph and performs this full relayout; Fit fits it to view. When the backend has more than 2,000 edges, the browser renders the strongest 2,000 and shows the rendered/total count; the complete edge set remains available to the backend. Legend distinguishes Skill, Record, Related, and Used-with edges. The graph refreshes automatically when memory or skills change, a learning job finishes, or the background graph build completes; automatic refreshes keep existing node positions and the current zoom/pan, laying out only additions, so the graph and the user's viewport stay still while idle. On narrow screens results and graph stack into separate bounded panes with their own scrollable result list.

- **Knowledge graph** (`#learning-graph`):
  - Type: visualization
  - Notes: Force-directed graph.

- **Reload graph** (`#learning-graph-refresh`):
  - Type: button
  - Action: Reloads the graph from the backend and performs a full relayout with node spacing.

- **Fit to view** (`#learning-graph-fit`):
  - Type: button
  - Action: Fits the graph to the viewport.

- **Delete selected graph node** (`#learning-graph-delete`):
  - Type: button
  - Action: Enabled when a memory record or learned skill node is selected. Deletes the selected node after a styled confirm, then reloads the graph and search results.

## Learning log

Learning-job activity feed from the trajectory log (experience extraction, consolidation, skill jobs, edge building, decay, prune), newest first. The feed reads one bounded 50-event page at a time instead of loading the full history; Previous and Next stay stable while new events arrive, and Refresh returns to the newest page. Job entries show a status badge (done or error); failures use a concise automatic-processing message rather than exposing a verbose provider body. Each entry has a compact Source <title> line, saved outcomes (kind + snippet) or an explicit Nothing to save. line, and per-type extras. A running indicator appears at the top while a learning job is in-flight.

- **Learning log count** (`#learning-log-count`):
  - Type: text

- **Refresh learning log** (`#learning-log-refresh`):
  - Type: button
  - Action: Reloads the newest bounded page of the learning-job trajectory feed.

- **Learning log** (`#learning-log`):
  - Type: list
  - Notes: Bounded 50-event trajectory page; job entries may have done/error/skipped status badges. Failed entries show a concise automatic-processing message; raw provider diagnostics stay server-side.

- **Previous learning log page** (`#learning-log-prev`):
  - Type: button
  - Action: Returns to the preceding newer 50-event page.

- **Learning log page number** (`#learning-log-page`):
  - Type: status

- **Next learning log page** (`#learning-log-next`):
  - Type: button
  - Action: Loads the next older 50-event page.
