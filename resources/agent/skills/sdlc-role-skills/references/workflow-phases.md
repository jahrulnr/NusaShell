# Workflow Phases Reference

Detailed phase-by-phase workflow for each SDLC role. Use when the user asks for the full sequence of a role's workflow, or when producing output that must follow every phase in order.

## Product Manager — 5-phase workflow

### Discovery
- Continuous competitor and regulation monitoring via Visualping and Perplexity AI for market research synthesis.
- Output: comparable feature/positioning brief, trend analysis.

### Define
- Cluster user interview transcripts via Dovetail.
- Stream into ChatPRD to produce an initial PRD draft covering goals, user stories, and acceptance criteria.
- Output: PRD draft.

### Build
- Validate visual concepts with rapid prototyping (Lovable) before engineering resource commitment.
- Draft technical specifications in Notion AI.
- Output: validated prototype, technical spec.

### Measure
- Analyze user behavior with Amplitude AI using natural-language queries; no manual funnel building.
- Output: behavior analysis, retention trends.

### Operate
- Synthesize meetings via Fathom.
- Protect deep-work time via Motion.
- Output: meeting summaries, protected calendar.

### Prompting pattern
PCTF — Persona, Context, Task, Format. Minimizes revisions; ensures house-style consistency.

## System Analyst — 4-phase workflow

### Context Engineering
- Author and maintain `PROJECT_CONTEXT.md` / `CLAUDE.md` / `AGENTS.md` in the repo.
- Document domain constraints, inference rules, decision architecture, glossary.
- Manage context window to prevent architectural hallucination.

### Specification
- Convert use-case diagrams and business rules into valid OpenAPI (YAML/JSON) specs via LLM.
- Generate ERDs and optimized data schemas.

### Edge Case & Gap Analysis
- Interrogate requirements with AI to find logic gaps, hidden dependencies, undefined boundary conditions.
- Simulate cross-system integration scenarios.

### Technical Handoff
- Translate abstract PRDs into structured technical tasks for backend and frontend engineers.

## Quality Assurance — 4-layer hybrid workflow

### Layer 1: Code-based foundation
- Playwright or Cypress as the code-based test foundation.

### Layer 2: Self-healing layer
- Healenium, Testim, or Mabl to auto-update selectors on minor UI/DOM changes.

### Layer 3: Visual AI
- Applitools Eyes or Percy on business-critical flows for layout shift and visual regression detection.

### Layer 4: Intelligent failure triage
- LLM-based log analysis to categorize failures as real code bugs, infra changes, or invalid scripts.
- Reduces MTTD.

### Maturity model (5 levels)
1. Manual testing
2. Brittle automation
3. AI-assisted testing
4. Predictive self-healing
5. Autonomous agentic

## Senior Backend Engineer — GSD 4-pass workflow

### Architecture Pass
- Instruct AI to design architecture patterns, detect distributed-system failure scenarios, and define database schemas before any code is written.

### Implementation Pass
- Write code with Cursor (cross-file repo understanding) or Claude Code (supervised terminal execution).
- Multi-file refactoring and terminal automation under professional oversight.

### Review Pass
- Require AI to self-critique generated code: validate error handling, security, and Clean Architecture compliance.

### Edge Case Pass
- Test under high load and race conditions.

### Output rule
All AI-generated code is a draft: must pass automated unit tests, linting, and security review before merge.

## Senior Frontend Engineer — 4-phase workflow

### Design extraction
- Encode Figma files and visual instructions into initial UI components (v0.dev, Lovable, Claude Design).

### IDE alignment
- Import draft components into Cursor or VS Code.
- Align with internal Design System, state management, and API data binding.

### Accessibility & performance
- Use AI to evaluate WCAG compliance and cross-device rendering optimization.

### Visual test integration
- Connect components to AI-based visual testing frameworks for interface consistency.

## Project Administrator — 3-pillar workflow

### Pillar 1: Meeting synthesis
- Granola, Otter.ai, or Catalist: record, transcribe, distribute decision points and action items directly into Jira or Linear.

### Pillar 2: Dynamic capacity & calendar
- Motion or Reclaim.ai: auto-reschedule tasks based on changing priorities; protect developer deep-work time.

### Pillar 3: Automated status reporting
- ClickUp Brain: summarize sprint progress, predict completion dates from burndown, generate weekly status reports without manual drafting.
