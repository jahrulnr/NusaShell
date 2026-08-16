# Research Toolchain Reference

Full 5-stage research pipeline with tool capabilities and human gates. Load this reference when the task involves literature research, evidence synthesis, or source-grounded writing.

## Stage 1: Plan

**Focus:** Formulate the research question; define inclusion and exclusion criteria for data.
**Primary tools:** Paperguide, Notion AI.
**Output:** Research protocol and variable taxonomy.
**Human gate:** The human reviews the research question for specificity, relevance, and empirical testability before advancing. AI can suggest framing, but the human owns the question.

## Stage 2: Search

**Focus:** Pull documents from indexed scientific repositories automatically.
**Primary tools:** PubMed, arXiv, OpenAlex, Semantic Scholar.
**Output:** Raw cross-discipline document corpus.
**Human gate:** The human confirms the corpus covers the right disciplines and time window; AI may pull broadly, but the human scopes.

## Stage 3: Screen

**Focus:** Filter thousands of abstracts against inclusion criteria automatically.
**Primary tools:** Elicit, Paperguide.
**Output:** Structured literature screening matrix.
**Human gate:** The human reviews the screening matrix for false excludes and false includes; AI screening is fast but can miss context the human catches.

### High-volume screening capability
Operate platforms like Elicit to scan thousands of abstracts automatically, extracting key parameters (study design, intervention, effect size) into a comparison matrix.

## Stage 4: Extract

**Focus:** Dissect methodology, sample size, and empirical data per document.
**Primary tools:** SciSpace Deep Review.
**Output:** Variable and statistics comparison table.
**Human gate:** The human verifies the extracted data against the source document; AI extraction can miss nuance in statistical methodology.

### Text interrogation capability
Use SciSpace Deep Review to ask exploratory natural-language questions against full PDF documents, extracting methodology details and research instruments directly from the text.

## Stage 5: Generate & Verify

**Focus:** Evaluate citation history, draft integrated synthesis, and verify claims.
**Primary tools:** Scite, Consensus, Zotero.
**Output:** Synthetic manuscript with verified citations.
**Human gate:** The human verifies every citation traces to a real primary source; AI-suggested references must be confirmed before use. This is the highest-risk stage for hallucination.

### Consensus mapping capability
Use Consensus to measure the percentage of scientific agreement around a claim, weighted by empirical evidence and journal reputation.

### Smart citation analysis capability
Use Scite (1.2B+ citation statements) to classify whether a publication is supported, disputed, or refuted by subsequent research.

### Integrated knowledge base capability
Combine Paperguide with reference management systems (Zotero, Mendeley) to ensure every citation binds to a verified original source (verified citation grounding).

## Human-in-the-Loop principle

AI handles mechanical stages (screening, extraction, initial drafting). The human handles:
- Critical evaluation of methodology and evidence.
- Identification of theory gaps and contradictions.
- Triangulation across quantitative, qualitative, and document-analysis methods.
- Final citation verification and ethical accountability.

The human is the strategic director, quality tester, and ethical owner. AI is the accelerator for administrative and initial-data-processing tasks.

## Citation style: inline hyperlinks, not footnotes

Use one consistent citation style across all formats: attach links directly inline at the relevant word or claim, not as numbered brackets (`[1]`, `[2]`) or end-of-document bibliographies.

Why this is the default:
- Numbered brackets feel encyclopedic — mismatched with the professional-but-human tone this skill targets.
- End bibliographies create friction: web-article readers expect to click directly when they meet a claim worth verifying, not scroll down to find a number then back up.
- Inline links are the convention for technical web writing (engineering blogs, documentation, tutorials) — most universal across formats.

Practice:
- **Specific tool/library/framework names** → link at first mention to the official source: npm page, GitHub/GitLab repo, or official docs.
- **Benchmark numbers/claims** → link near the number, not in a separate footnote. Example: "...dropped to 340ms, consistent with [TechEmpower benchmark](https://...) results for a similar pattern" — not "...dropped to 340ms [3]" with `[3]` explained below.
- **Do not over-link.** Only link terms that genuinely need external reference (specific product names, claimed numbers from other sources, technical standards/specs). Common words or basic concepts ("database", "API") do not need links.
- **Do not fabricate URLs.** If unsure of the exact URL or unable to verify, state the source name as plain text without a hyperlink, or find the correct link before attaching one.
- **One term linked once** per article, usually at first mention — do not re-link the same name every time it appears.
