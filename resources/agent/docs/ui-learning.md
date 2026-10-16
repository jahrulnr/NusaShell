# Learning

The learning workbench: memory documents (About you / About agent), the searchable experience and record store, the knowledge graph atlas, and the learning activity log behind four tabs.

**How to open:** Click the Learning item in the left rail, or open #learning.

## Header stats

Live counters injected into the legend: memory items and graph edges (rendered vs total when the edge cap applies).

- **Memory count** (`learning-stat-memory`):
  - Type: text

- **Edge count** (`learning-stat-edges`):
  - Type: text

## Tabs

Four tabs: Overview (stats, brief, recent learning, map), Memory (About you / About agent editors), Experiences (search + record lists), Activity (learning log). At widths through 720px the Overview tab collapses its document editors and knowledge map behind disclosure toggles, and the Memory atlas and Experiences tabs show either their list or one focused detail pane with a controller-mounted Back control.

Refresh spins during the full request and stops after completion or failure; sync status is announced nonvisually. On mobile, episode and Activity panels follow natural content height with page scrolling.

- **Overview tab** (`#learning-tab-overview`):
  - Type: tab
  - Action: Shows stats, the carried brief, recent learning, and the knowledge map.

- **Memory tab** (`#learning-tab-memory`):
  - Type: tab
  - Action: Opens the About you / About agent editors.
  - Notes: Shows the editable always-injected user memory document.

- **Experiences tab** (`#learning-tab-experiences`):
  - Type: tab
  - Action: Opens experience and memory record search.
  - Notes: Shows recorded episodes and structured memory records.

- **Activity tab** (`#learning-tab-activity`):
  - Type: tab
  - Action: Opens the learning activity log.
  - Notes: Shows the learning-job activity feed.

- **Refresh Learning** (`#learning-refresh`):
  - Type: button
  - Action: Refreshes Learning data; the icon spins until memory, graph and loaded activity/search requests finish, then stops, including on failure.

- **Learning sync status** (`#learning-sync-state`):
  - Type: status
  - Notes: Nonvisual live sync status; no status label takes space beside the tabs.

## About You

The editable user memory document. Reload reads the persisted value again; Save changes replaces the complete user memory document through memory.user.update. The editor shows the 4000-character cap and allows an intentional empty document. The card expands across the available desktop canvas while keeping a compact inset and full-width editor on mobile. At widths through 720px the card collapses to a compact entry point behind an Edit disclosure toggle; status chips stay visible and an unsaved draft survives toggles.

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

- **Edit memory document** (`.learning-doc-toggle`):
  - Type: button
  - Action: Expands or collapses the card's memory editor (Edit/Done); the draft text, dirty flag, and character counter survive toggling.
  - Notes: One per .learning-document-card, mounted by the learning controller; the collapsed state only applies at widths through 720px — desktop keeps both editors expanded.

## About Agent

The editable agent-tier memory document. Reload reads the persisted value again; Save changes replaces the complete soul.md document through memory.agent.update. The editor shows the 4000-character cap and allows an intentional empty document. Changes remain separate from user.md and memory records. At widths through 720px the card collapses to a compact entry point behind an Edit disclosure toggle; status chips stay visible and an unsaved draft survives toggles.

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

- **Edit memory document** (`.learning-doc-toggle`):
  - Type: button
  - Action: Expands or collapses the card's memory editor (Edit/Done); the draft text, dirty flag, and character counter survive toggling.
  - Notes: One per .learning-document-card, mounted by the learning controller; the collapsed state only applies at widths through 720px — desktop keeps both editors expanded.

## Experience

Paginated list of recorded episodes from experience.list (newest first, 25 per page, Prev/Next controls). Each row shows the goal, outcome status, signals, and timestamp, with a per-row delete control that calls experience.delete after a styled confirm. Clicking a row loads experience.get and expands detail. Structured memory records from memory.list sit beside it; a selected record can be deleted with memory.delete after a styled confirm. The lists refresh on experience.recorded, experience.deleted, learning.job.started/done/error, and memory.updated. At widths through 720px an opened episode takes the focused detail pane; Back to episodes returns to the list, and re-opening the loaded episode or deleting it collapses back to the list.

- **Experience list** (`#learning-experience-list`):
  - Type: list
  - Notes: Read-only episodes from experience.list, newest first. Click a row to load experience.get.

- **Memory records list** (`#learning-records-list`):
  - Type: list
  - Notes: Structured MemoryRecord rows from memory.list (tier record). Select a row, then delete.

- **Delete memory record** (`#learning-record-delete`):
  - Type: button
  - Action: Calls memory.delete after a styled confirm. Removes the record, its graph edges, and its retrieval presence permanently. Disabled until a record is selected.

- **Back to episodes** (`#learning-experience-back`):
  - Type: button
  - Action: Returns to the episode list while keeping the loaded episode and page position.
  - Notes: Mounted by the learning controller at the top of the experiences layout; visible only at widths through 720px.

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

Ranked search results with a count. With an empty query the pane lists all skills and records so content is visible immediately. Memory hits show a tier badge (user, agent, or record) next to the kind label. Records are retired from the Experience tab, not deleted here. Long content is collapsed to 3 lines; click to expand. Results refresh automatically when memory or skills change (via `memory.updated` / `skill.updated` events, debounced 300ms). At widths through 720px selecting a result opens it as a focused detail pane over the atlas; Back to atlas returns to the filtered result list and restores its scroll position.

- **Results** (`#learning-results`):
  - Type: list

- **Results count** (`#learning-results-count`):
  - Type: text

- **Back to atlas** (`#learning-atlas-back`):
  - Type: button
  - Action: Returns to the atlas result list while keeping the selected record, query, and kind filter.
  - Notes: Mounted by the learning controller at the top of the atlas layout; visible only at widths through 720px.

## Splitter

The atlas splitter divides the list column from the graph; it drags between 180px and viewport-200, steps 16px on arrow keys, and persists (clears below 860px).

- **Atlas splitter** (`learning-splitter`):
  - Type: separator

## Knowledge graph

Force-directed graph (vis-network) of skills, memory records, and their edges. Related edges are rebuilt from content similarity plus record metadata; stale edges to deleted nodes are removed. Used-with edges connect learning nodes observed together during one successful agent or learning-job turn. The backend coalesces unchanged graph rebuilds and emits a refresh event when newly derived edges are ready. Dense edge lines stay thin. Node size scales with the number of unique neighbouring nodes, making the most-connected hubs largest; hover text includes the relation count. Zooming out preserves most of that size contrast so nearby relation counts remain visually distinct instead of collapsing to the same rasterized radius. After its bounded physics pass, a full layout keeps the force-directed angular grouping but places highly connected hubs toward the center and low-degree or isolated nodes toward a compact perimeter ring without detaching them from the main cluster, then resolves residual collisions before freezing. The restrained archipelago palette maps skills to ocean blue, records to earth brown, user memory to leaf green, and edges to deeper ocean, mangrove, or sand tones. Reload fetches the graph and performs this full relayout; Fit fits it to view. When the backend has more than 2,000 edges, the browser renders the strongest 2,000 and shows the rendered/total count; the complete edge set remains available to the backend. Legend distinguishes Skill, Record, Related, and Used-with edges. The graph refreshes automatically when memory or skills change, a learning job finishes, or the background graph build completes (via `memory.updated` / `skill.updated` / `learning.job.done` / `learning.graph.updated` events, coalesced with a 300ms debounce); automatic refreshes keep existing node positions and the current zoom/pan, laying out only additions, so the graph and the user's viewport stay still while idle. On narrow screens results and graph stack into separate bounded panes with their own scrollable result list. On the Overview tab at widths through 720px the map card collapses behind a Show map disclosure that redraws and refits the graph when revealed.

The Overview map and Activity feed share the global Learning Refresh; they do not render duplicate refresh buttons. Global refresh retains the current graph viewport and Activity page.

- **Knowledge graph** (`#learning-graph`):
  - Type: visualization
  - Notes: vis-network force-directed graph.

- **Fit to view** (`#learning-graph-fit`):
  - Type: button
  - Action: Fits the graph to the viewport.

- **Show map** (`.learning-map-toggle`):
  - Type: button
  - Action: Reveals or hides the overview knowledge-map preview (Show map/Hide map); revealing refits the graph into the now-visible frame.
  - Notes: Mounted by the learning controller on the overview map card; the collapsed state only applies at widths through 720px.

## Learning log

Learning-job activity feed from the trajectory log (experience extraction, consolidation, skill jobs, edge building, decay, prune), newest first. The feed reads one bounded 50-event page at a time instead of loading the full history; Previous and Next use stable cursors; the single Learning Refresh reloads the current page alongside the other Learning data. Job entries show a status badge (done or error); failures use a concise automatic-processing message rather than exposing a verbose provider body. Each entry has a compact Source <title> line, saved outcomes (kind + snippet) or an explicit Nothing to save. line, and per-type extras. A running indicator appears at the top while a learning job is in-flight. Inert recent-job status chips are not displayed. At widths through 720px the feed flows in the single page scroll instead of a nested scroll cage.

- **Learning log count** (`#learning-log-count`):
  - Type: text

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
  - Action: Loads the next older 50-event page with the trajectory cursor.
