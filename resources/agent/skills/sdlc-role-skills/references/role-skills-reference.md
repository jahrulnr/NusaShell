# Role Skills Reference

Full AI skill taxonomy per SDLC role, with cited sources from the original research report. Use this as the canonical reference when a role section in `SKILL.md` needs deeper detail or when the user asks for the source of a skill claim.

## Product Manager (PM)

### Skill 1: Generative Documentation & PRD Engineering
Operate specialist product writing tools (ChatPRD, Notion AI). Apply structured prompt engineering with the PCTF framework (Persona, Context, Task, Format) to produce PRDs, user stories, and acceptance criteria consistently. Upload historical documents to guide AI in learning the organization's house writing style. [4, 7]

### Skill 2: Customer Research & Feedback Synthesis
Extract insights from dozens of user interview transcripts automatically (Dovetail, Productboard AI, Kraftful). Identify Jobs-to-be-Done (JTBD) patterns and cluster customer feedback by sentiment and impact frequency. [4, 7]

### Skill 3: Automated Market & Competitive Intelligence
Build automated workflows for monitoring competitor sites and regulatory changes (Visualping, Perplexity AI). Produce comparative feature analysis, market positioning, and industry trend analysis via AI synthesis. [4]

### Skill 4: Rapid Prototyping & Generative Product Design
Design interactive application prototypes from text descriptions (Lovable, v0.dev) to validate early concepts before engineering handoff. Use Gamma to convert written PRD specs into stakeholder presentation decks automatically. [4]

### Skill 5: Natural Language Product Analytics
Operate natural-language analytics platforms (Amplitude AI) to evaluate user journeys and retention trends without building manual queries. [4]

## System Analyst (SA)

### Skill 1: System Context Repository Engineering
Design and maintain structured context architecture files (`PROJECT_CONTEXT.md`, `CLAUDE.md`) that define domain constraints, inference rules, and glossary for processing by developer AI agents. Includes context window management so AI inference on large-scale systems stays accurate and free of architectural hallucination. [5]

### Skill 2: Automated Specification & API Contract Modeling
Transform business rules and application workflows into precisely-notated OpenAPI (Swagger) specs using LLMs. Generate Entity Relationship Diagrams (ERDs) and optimized data schema structures via AI synthesis. [12, 15]

### Skill 3: AI-Assisted Edge Case & Gap Analysis
Use AI cognitive analysis to interrogate system requirements documents for logic gaps, dependency conflicts, and undefined boundary conditions. Includes cross-system integration scenario construction and complex transaction flow mapping with AI simulation. [1, 5]

### Skill 4: Technical Handoff Translation
Translate abstract product requirement documents (PRDs) into structured technical tasks decomposed for consumption by backend and frontend teams. [1]

## Quality Assurance (QA)

### Skill 1: Self-Healing Test Automation
Implement and configure self-healing locator-based test scripts (Healenium, Testim, Mabl). Auto-recover flaky tests from UI/DOM attribute changes inside CI/CD pipelines. [6, 8]

### Skill 2: AI Visual Regression Testing
Integrate Visual AI SDKs (Applitools Eyes, Percy) into Playwright, Cypress, or Selenium frameworks. Configure visual checkpoints to validate layout accuracy, responsive support, and dynamic rendering across devices. [8, 13]

### Skill 3: Agentic & Autonomous Test Generation
Use AI-native and NLP-based testing tools (Postbot on Postman, testRigor, Testomat.io) to generate functional and API test scripts from natural-language descriptions. Direct autonomous exploratory testing agents to find regression scenarios in complex user flows. [3]

### Skill 4: Intelligent Log & Failure Analysis
Implement LLM-based failure analysis systems to triage execution logs, separate code bugs from environment issues, and accelerate Mean Time to Diagnosis (MTTD). [8]

## Senior Backend Engineer

### Skill 1: Context Engineering & Meta-Prompting
Design tiered prompt systems (meta-prompting) that deliver persona, security constraints, and architecture patterns precisely to AI coding tools. Maintain context repositories (`AGENTS.md`, `CLAUDE.md`) so AI understands coding conventions, dependencies, and team performance standards. [5]

### Skill 2: Agentic IDE & Terminal Execution
Operate agentic coding tools: Cursor (with full codebase indexing), Claude Code (supervised agentic terminal), GitHub Copilot Workspace. Execute multi-file engineering tasks (refactoring) and terminal command automation with professional oversight. [14]

### Skill 3: Automated Code Quality, Review & Security Hardening
Direct AI to execute local security analysis (vulnerability detection), memory optimization, and race condition resolution using integrated tools (Snyk Code, Qodo). Iterative refinement to ensure every generative code output meets unit testing coverage standards (pytest, Go test). [5, 25]

### Skill 4: Distributed Systems Pattern Synthesis
Use AI to simulate and design distributed system patterns: Event Sourcing, CQRS, Rate Limiting, and advanced database schema modeling. [16]

## Senior Frontend Engineer

### Skill 1: Generative Design-to-Code Pipelines
Master the workflow of converting Figma design assets into clean, modular UI component code (v0.dev, Lovable, Claude Design). Convert generative prototype output into React, Next.js, or Vue.js architecture that meets production quality standards. [17]

### Skill 2: Component Architecture & Design System Integration
Align AI-generated components with the internal company Design System, ensuring separation of presentational components and container components. Use AI to map complex application state (state management) and API data binding efficiently. [17]

### Skill 3: AI-Powered Accessibility & Performance Tuning
Instruct AI to evaluate and fix DOM structure to meet WCAG criteria (accessibility), provide ARIA attributes, and optimize Core Web Vitals. Refactor code to prevent unnecessary re-renders in large-scale interface applications. [13, 19]

## Project Administrator (PA)

### Skill 1: Automated Meeting & Decision Intelligence
Operate smart note-taking tools (Granola, Fathom, Otter.ai) to auto-capture meeting transcripts. Extract decision points, project risks, and action item summaries, and sync them into Jira or Asana automatically. [4]

### Skill 2: Dynamic Resource & Schedule Optimization
Operate automated time management tools (Motion, Reclaim.ai) to manage team task priorities, adjust dynamic deadline schedules, and protect developer deep-work time. [4]

### Skill 3: Automated Executive Reporting
Use task analytics tools (ClickUp Brain, Catalist) to summarize sprint status, identify blockers, and generate weekly progress reports automatically. [20]

## Sources

The numbered citations `[n]` refer to the original research report's bibliography, preserved in `tmp/plan/Panduan Keterampilan AI Berbagai Peran.md`.
