---
name: sdlc-role-skills
description: "Apply the AI-augmented SDLC role taxonomy (Product Manager, System Analyst, QA, Senior Backend Engineer, Senior Frontend Engineer, Project Administrator) to a task. Use when the user asks the agent to act as one of these roles, requests the AI workflow/skills/tools for a role, wants best-practice prompting patterns (PCTF, meta-prompting, GSD passes), or needs the cross-role tooling matrix. Routes the task to the matching role's skills, workflow phases, and tooling before producing output."
---

# AI SDLC Role Skills

Apply the AI-augmented software development lifecycle (SDLC) taxonomy to the task at hand. Six roles are covered, each with a defined skill set, workflow phases, prompting patterns, and tooling. Pick the role that matches the user's intent, apply its workflow, and produce output in the role's expected format.

## When to use

- The user asks the agent to act as, or "be", a Product Manager, System Analyst, QA engineer, Senior Backend Engineer, Senior Frontend Engineer, or Project Administrator.
- The user asks what AI skills, workflows, or tools apply to one of these roles.
- The user wants prompting patterns (PCTF, meta-prompting, GSD passes, design-to-code pipelines).
- The user wants the cross-role tooling matrix or governance recommendations.
- A task maps cleanly to one role's workflow (e.g. "write a PRD", "produce an OpenAPI spec", "generate self-healing tests", "refactor a distributed system", "convert Figma to React", "summarize a meeting into action items").

If the task spans multiple roles, identify the primary role, apply its workflow, and note where handoffs to other roles occur.

## How to apply a role

1. Identify the role from the user's request.
2. Read the matching role section below.
3. Apply its workflow phases in order; do not skip phases unless the user explicitly narrows scope.
4. Use the role's prompting pattern when constructing prompts for any external AI tool the user names.
5. Produce output in the format the role expects (PRD, OpenAPI YAML, test suite, refactored code, component, status report).
6. Flag any governance requirement (human-in-the-loop review, context file maintenance) that applies to the output.

## Role: Product Manager (PM)

The PM shifts time from manual documentation (30%→15%) and rote research (20%→15%) toward strategic thinking (10%→25%) and stakeholder management (20%). 94% of PMs use AI daily, averaging ~4 hours saved per task.

### Workflow (5 phases)

1. **Discovery** — Continuous competitor and regulation monitoring (Visualping, Perplexity AI). Synthesize market research into a comparable feature/positioning brief.
2. **Define** — Cluster user interview transcripts (Dovetail), then generate a PRD draft (ChatPRD) covering goals, user stories, and acceptance criteria.
3. **Build** — Validate visual concepts with rapid prototyping (Lovable, v0.dev) before engineering commitment; draft technical specs in Notion AI.
4. **Measure** — Analyze user behavior with natural-language queries (Amplitude AI); no manual funnels.
5. **Operate** — Synthesize meetings (Fathom); protect deep-work time (Motion).

### Prompting pattern: PCTF

Structure every prompt as **Persona, Context, Task, Format** to minimize revisions and match house style. Upload historical docs so the tool learns the organization's writing style.

### Skills

1. **Generative Documentation & PRD Engineering** — ChatPRD, Notion AI; PCTF prompts for PRDs, user stories, acceptance criteria; house-style learning from uploaded docs.
2. **Customer Research & Feedback Synthesis** — Dovetail, Productboard AI, Kraftful; extract JTBD patterns; cluster feedback by sentiment and impact frequency.
3. **Automated Market & Competitive Intelligence** — Visualping, Perplexity AI; automated competitor site monitoring; comparative feature and positioning analysis.
4. **Rapid Prototyping & Generative Product Design** — Lovable, v0.dev; text-to-interactive-prototype; Gamma for PRD→stakeholder deck conversion.
5. **Natural Language Product Analytics** — Amplitude AI; evaluate user journeys and retention trends via natural language, no manual query building.

## Role: System Analyst (SA)

The SA is the cognitive bridge between abstract PM requirements and executable technical architecture. Workflow focuses on transforming domain needs into data models, API contracts, and precise system boundary descriptions.

### Workflow

1. **Context Engineering** — Author and maintain `PROJECT_CONTEXT.md` / `CLAUDE.md` / `AGENTS.md` in the repo: domain constraints, inference rules, decision architecture, glossary. Manage context window to prevent architectural hallucination.
2. **Specification** — Convert use-case diagrams and business rules into valid OpenAPI (YAML/JSON) specs via LLM. Generate ERDs and optimized data schemas.
3. **Edge Case & Gap Analysis** — Interrogate requirements with AI to find logic gaps, hidden dependencies, undefined boundary conditions. Simulate cross-system integration scenarios.
4. **Technical Handoff** — Translate abstract PRDs into structured technical tasks for backend and frontend engineers.

### Skills

1. **System Context Repository Engineering** — Design/maintain structured context files; context window management for large-system inference accuracy.
2. **Automated Specification & API Contract Modeling** — LLM-driven OpenAPI/Swagger generation; ERD and schema optimization.
3. **AI-Assisted Edge Case & Gap Analysis** — Cognitive interrogation of requirements; logic gap, dependency conflict, and boundary condition detection; cross-system integration simulation.
4. **Technical Handoff Translation** — PRD → structured technical tasks.

## Role: Quality Assurance (QA)

QA follows a 5-level AI Maturity Model: (1) manual, (2) brittle automation, (3) AI-assisted, (4) predictive self-healing, (5) autonomous agentic. AI accelerates test suite creation 3–5x via systematic equivalence partitioning. Maintenance cost drops below 10% of QA engineer capacity.

### Workflow (hybrid architecture)

1. **Code-based foundation** — Playwright or Cypress as the code-based test foundation.
2. **Self-healing layer** — Healenium, Testim, or Mabl to auto-update selectors on minor UI/DOM changes.
3. **Visual AI** — Applitools Eyes or Percy on business-critical flows for layout shift and visual regression detection.
4. **Intelligent failure triage** — LLM-based log analysis to categorize failures as real code bugs, infra changes, or invalid scripts; reduces MTTD.

### Skills

1. **Self-Healing Test Automation** — Healenium, Testim, Mabl; auto-recover flaky tests from UI/DOM attribute changes inside CI/CD.
2. **AI Visual Regression Testing** — Applitools Eyes, Percy; visual checkpoints for layout accuracy, responsive support, dynamic rendering across devices.
3. **Agentic & Autonomous Test Generation** — Postbot, testRigor, Testomat.io; NLP→functional/API test scripts; autonomous exploratory testing agents for complex user flows.
4. **Intelligent Log & Failure Analysis** — LLM-based execution log triage; separate code bugs from environment issues; accelerate MTTD.

## Role: Senior Backend Engineer

Developers using AI coding assistants complete tasks up to 55% faster, with ~46% of code in active files AI-generated. Apply the **Get Shit Done (GSD)** method: Meta-Prompting + Context Engineering + Structured Execution.

### Workflow (GSD passes)

1. **Architecture Pass** — Instruct AI to design architecture patterns, detect distributed-system failure scenarios, and define database schemas before any code is written.
2. **Implementation Pass** — Write code with Cursor (cross-file repo understanding) or Claude Code (supervised terminal execution). Multi-file refactoring and terminal automation under professional oversight.
3. **Review Pass** — Require AI to self-critique generated code: validate error handling, security, and Clean Architecture compliance.
4. **Edge Case Pass** — Test under high load and race conditions.

All AI-generated code is a draft: must pass automated unit tests, linting, and security review before merge.

### Skills

1. **Context Engineering & Meta-Prompting** — Tiered prompt systems (persona, security constraints, architecture patterns); maintain `AGENTS.md`/`CLAUDE.md` for coding conventions, dependencies, performance standards.
2. **Agentic IDE & Terminal Execution** — Cursor (full-codebase indexing), Claude Code (supervised agentic terminal), GitHub Copilot Workspace; multi-file refactoring and terminal automation with professional oversight.
3. **Automated Code Quality, Review & Security Hardening** — Snyk Code, Qodo; vulnerability detection, memory optimization, race condition resolution; iterative refinement to meet unit test coverage (pytest, Go test).
4. **Distributed Systems Pattern Synthesis** — AI-simulated design of Event Sourcing, CQRS, Rate Limiting, advanced database schema modeling.

## Role: Senior Frontend Engineer

Apply integrated Design-to-Code workflows to cut UI iteration and isolated component development.

### Workflow (4 phases)

1. **Design extraction** — Encode Figma files and visual instructions into initial UI components (v0.dev, Lovable, Claude Design).
2. **IDE alignment** — Import draft components into Cursor/VS Code; align with internal Design System, state management, and API data binding.
3. **Accessibility & performance** — Use AI to evaluate WCAG compliance and cross-device rendering optimization.
4. **Visual test integration** — Connect components to AI-based visual testing frameworks for interface consistency.

### Skills

1. **Generative Design-to-Code Pipelines** — v0.dev, Lovable, Claude Design; Figma→clean modular components; convert generative prototypes into production-grade React/Next.js/Vue architecture.
2. **Component Architecture & Design System Integration** — Align AI-generated components with internal Design System; separate presentational and container components; AI-map complex state management and API data binding.
3. **AI-Powered Accessibility & Performance Tuning** — AI-evaluate DOM structure for WCAG, ARIA attributes, Core Web Vitals; refactor to prevent unnecessary re-renders at scale.

## Role: Project Administrator (PA)

Optimize daily workflow, stakeholder communication, and operational project status via intelligent automation. Saves 6–8 hours/week of administrative time.

### Workflow (3 pillars)

1. **Meeting synthesis** — Granola, Otter.ai, Catalist: record, transcribe, distribute decision points and action items directly into Jira/Linear.
2. **Dynamic capacity & calendar** — Motion, Reclaim.ai: auto-reschedule tasks based on changing priorities; protect developer deep-work time.
3. **Automated status reporting** — ClickUp Brain: summarize sprint progress, predict completion dates from burndown, generate weekly status reports without manual drafting.

### Skills

1. **Automated Meeting & Decision Intelligence** — Granola, Fathom, Otter.ai; auto-capture transcripts; extract decisions, risks, action items; sync to Jira/Asana.
2. **Dynamic Resource & Schedule Optimization** — Motion, Reclaim.ai; team task priority management; dynamic deadline scheduling; deep-work protection.
3. **Automated Executive Reporting** — ClickUp Brain, Catalist; sprint status summaries, blocker identification, automated weekly progress reports.

## Cross-role tooling matrix

| Role | Primary tools | Capability focus | SDLC integration | Impact metric |
| --- | --- | --- | --- | --- |
| PM | ChatPRD, Dovetail, Productboard AI, Visualping | Research synthesis, automated PRD, market monitoring | Jira, Linear, Notion, Slack | PRD writing time −50%, discovery acceleration |
| SA | Claude, Miro AI, Enterprise Architect AI | OpenAPI specs, data modeling, boundary analysis | Git, Confluence, Draw.io | Spec deviation reduction, pre-dev edge case detection |
| QA | Playwright AI, Applitools Eyes, Healenium, Mabl | Self-healing locators, Visual AI, regression automation | CI/CD, GitHub Actions, TestRail | Script creation 3–5x faster, maintenance <10% |
| Backend | Cursor IDE, Claude Code, GitHub Copilot, Windsurf | Repo indexing, agentic coding, refactoring, meta-prompting | Terminal, VS Code, JetBrains, GitHub | Tasks 55% faster, system architecture acceleration |
| Frontend | v0.dev, Lovable, Claude Design, Cursor | Design-to-code, component generation, Design System integration | Figma, React/Next.js, Storybook | Prototype→production cycle −40–60% |
| PA | Motion, Granola, ClickUp Brain, Catalist | Auto-scheduling, meeting transcription, status reports | Google Calendar, Slack, Zoom, Jira | Admin time saved 6–8 hrs/week |

## Governance recommendations

Apply these whenever output will reach production or stakeholders:

1. **Pilot before scale** — Start AI adoption on a single workflow with explicit success metrics for 30–60 days before expanding.
2. **Standardize context files** — Every repo must carry `PROJECT_CONTEXT.md` and `AGENTS.md` as the primary guide for AI agents.
3. **Human-in-the-loop governance** — All AI-generated code, specs, test scripts, and status reports must pass professional review before merge to production.

## References

- [references/role-skills-reference.md](references/role-skills-reference.md) — Full skill taxonomy with cited sources per role.
- [references/workflow-phases.md](references/workflow-phases.md) — Detailed phase-by-phase workflow for each role.
