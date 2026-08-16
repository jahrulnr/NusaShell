# Learning

Search accumulated memory entries and skills via hybrid BM25 + embedding + graph search, and explore how they connect in the knowledge graph.

**How to open:** Click the Learning item in the left sidebar.

## Header stats

Live counts of memory entries and learning edges.

- **Memory count** (`#learning-stat-memory`):
  - Section: Learning
  - Type: text

- **Edge count** (`#learning-stat-edges`):
  - Section: Learning
  - Type: text

## Search bar

Hybrid search across memory and skills, with a kind filter (All, Skills, Memory) and a Search button.

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

Ranked search results with a count. With an empty query the pane lists all skills and memories so content is visible immediately.

- **Results** (`#learning-results`):
  - Section: Learning
  - Type: list

- **Results count** (`#learning-results-count`):
  - Section: Learning
  - Type: text

## Knowledge graph

Force-directed graph (vis-network) of skills, memory entries, and their edges. Refresh reloads the graph; Fit fits it to view. Legend distinguishes Skill, Memory, Related, and Used-with edges.

- **Knowledge graph** (`#learning-graph`):
  - Section: Learning
  - Type: visualization
  - Notes: vis-network force-directed graph.

- **Reload graph** (`#learning-graph-refresh`):
  - Section: Learning
  - Type: button
  - Action: Reloads the graph from the backend.

- **Fit to view** (`#learning-graph-fit`):
  - Section: Learning
  - Type: button
  - Action: Fits the graph to the viewport.
