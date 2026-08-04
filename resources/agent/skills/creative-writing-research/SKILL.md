---
name: creative-writing-research
description: "Apply human-driven writing and research methodology to a creative or academic task. Use when the user asks to write a blog post, journal article, academic paper, feature article, news report, or story (fiction/memoir/screenplay), conduct literature research, synthesize sources, or wants the Human-in-the-Loop workflow that keeps human cognitive value (voice, subtext, source verification) while using AI for mechanical stages. Routes the task to the matching genre's workflow, the 5-stage research pipeline, and the stylometry checklist before producing output."
---

# Creative Writing & Research

Apply human-driven writing and research methodology to the task. Five writing genres and one research workflow are covered. Pick the genre or workflow that matches the user's intent, apply its phases, and produce output that preserves human cognitive value while delegating only mechanical stages to AI.

## When to use

- The user asks the agent to write or draft a blog post, journal/academic article, feature article, news report, or story (fiction, memoir, screenplay).
- The user asks to conduct literature research, screen sources, extract data, or synthesize a review.
- The user wants the Human-in-the-Loop (HITL) workflow that keeps human cognitive value while using AI for mechanical stages.
- The user wants the stylometry checklist to evaluate whether output reads as human-authored.
- The user asks for the writing-genre vs. AI-substitution-risk matrix or the research toolchain.

If the task spans multiple genres, identify the primary genre, apply its workflow, and note where handoffs occur.

## How to apply

1. Identify the genre or workflow from the user's request.
2. Read the matching section below; load the reference file only when the current task needs the full detail.
3. Apply the workflow phases in order; do not skip phases unless the user explicitly narrows scope.
4. Run the stylometry checklist on any draft before delivering it.
5. Flag any HITL boundary that applies: source verification, ethical accountability, or original-voice ownership must stay with the human.

## Genre: Blog Writing

**Human edge:** personal voice, unique angle, emotional engagement with the reader.
**AI weakness:** cliché hooks, uniform styling, lack of real examples.

### Workflow

1. **Angle formulation** — Find a unique perspective or gap in public discourse that cannot be replicated by automation. State the angle in one sentence before drafting.
2. **Voice & tone modulation** — Set the voice to match brand identity and the emotional register the audience expects. Keep it consistent across the piece.
3. **Hook engineering** — Open with a real story or real dilemma that triggers curiosity. Avoid generic AI-style openings ("In today's fast-paced world…").
4. **Technical translation** — Translate complex concepts or data into everyday persuasive narrative without sacrificing baseline accuracy.
5. **Stylometry pass** — Run the 5-parameter checklist (see below); revise any parameter that reads as AI-flattened.

## Genre: Journal & Academic Writing

**Human edge:** logical rigor, hypothesis formulation, honest evaluation of evidence limits.
**AI weakness:** pragmatic context gaps, citation hallucination.

### Workflow

1. **Theoretical framework** — Build the framework and formulate hypotheses that connect research variables explicitly.
2. **Literature critique** — Do not merely summarize prior publications. Evaluate methodological weaknesses, sample bias, and epistemological gaps.
3. **Register calibration** — Use formal scientific register that is distanced yet communicative; present findings precisely without falling into automated-text redundancy.
4. **Citation grounding** — Every citation must trace to a verified primary source. Use the research toolchain (see below) for discovery, but verify each reference manually.
5. **Stylometry pass** — Run the checklist; academic text is especially vulnerable to AI-style semantic flattening.

## Genre: Feature Article Writing

**Human edge:** cross-disciplinary synthesis, macro-trend interpretation, second- and third-order implications.
**AI weakness:** shallow generalization, no standpoint insight.

### Workflow

1. **Expert synthesis** — Extract insight from expert interviews, blend direct quotes, and build data-driven narrative.
2. **Issue framing** — Connect micro incidents to broader social, economic, or technology phenomena.
3. **Implication chain** — Draw second- and third-order implications that an algorithm without real-world understanding cannot reach.
4. **Stylometry pass** — Run the checklist.

## Genre: News Writing

**Human edge:** field investigation, ethical verification, social understanding.
**AI weakness:** cannot verify primary sources directly.

### Workflow

1. **Primary source verification** — Check claims through eyewitness interviews, official document analysis, and cross-investigation. This stage is non-delegable to AI.
2. **Inverted pyramid** — Prioritize the most important facts in the lead paragraph while preserving balanced context.
3. **Ethical balance** — Cover both sides; eliminate personal or algorithmic bias.
4. **Stylometry pass** — Run the checklist; news text must read as neutral and grounded, not AI-smooth.

## Genre: Story Writing (Fiction, Memoir, Screenplay)

**Human edge:** emotional subtext, rich metaphor, complex character architecture.
**AI weakness:** stylistic flattening, predictable plot, no lived experience.

### Workflow

1. **Character architecture** — Construct characters with internal motivation, trauma, moral contradiction, and gradual psychological growth.
2. **Subtext & implication** — Deliver deepest meaning through symbolism, implied dialogue, and scene atmosphere ("show, don't tell").
3. **Pacing & tension** — Control narrative tempo and dramatic tension intuitively; direct the emotional climax to trigger empathy and reflection.
4. **Stylometry pass** — Run the checklist; story text is the most vulnerable to AI flattening.

## Research Workflow (5-stage pipeline)

Use when the task is literature research, evidence synthesis, or source-grounded writing. AI handles mechanical stages; the human handles critical evaluation and verification.

| Stage | Focus | Primary tools | Output |
| --- | --- | --- | --- |
| 1. Plan | Formulate research question; define inclusion/exclusion criteria | Paperguide, Notion AI | Research protocol, variable taxonomy |
| 2. Search | Pull documents from indexed scientific repositories | PubMed, arXiv, OpenAlex, Semantic Scholar | Raw cross-discipline document corpus |
| 3. Screen | Filter thousands of abstracts against inclusion criteria | Elicit, Paperguide | Structured literature screening matrix |
| 4. Extract | Dissect methodology, sample size, empirical data per document | SciSpace Deep Review | Variable and statistics comparison table |
| 5. Generate & Verify | Evaluate citation history, draft integrated synthesis, verify claims | Scite, Consensus, Zotero | Synthetic manuscript with verified citations |

**Human gate at every stage:** the human reviews the AI's screening, extraction, and synthesis for methodological bias, missed context, or hallucinated citations before advancing.

For the full toolchain capability list, read `references/research-toolchain.md`.

## Stylometry checklist (run before delivery)

Compare the draft against the 5 parameters that distinguish human writing from AI output:

1. **Grammatical & syntactic variability** — Does sentence length and clause structure vary dynamically? AI defaults to uniform, predictable syntax.
2. **Semantic richness** — Are metaphors specific and rooted in cultural/sensory context? AI defaults to statistical word associations and cliché metaphor.
3. **Pragmatic relevance** — Does the text account for social context, reader emotion, and local cultural implication? AI operates in probability space without real pragmatic understanding.
4. **Rhetorical organization** — Does the argument flow non-linearly with logical surprise yet stay coherent? AI defaults to rigid standard expository schema.
5. **Style & interpersonal engagement** — Does the writing radiate awareness, empathy, and subjective stance? AI maintains a formal, distanced, objective tone.

Revise any parameter that reads as AI-flattened before delivering the draft.

For the full parameter reference and substitution-risk matrix, read `references/stylometry-checklist.md`.

## HITL boundaries (non-delegable to AI)

- **Source verification** — Primary source checks (interviews, documents, cross-investigation) stay with the human.
- **Ethical accountability** — Balanced reporting, bias elimination, and moral judgment stay with the human.
- **Original voice ownership** — Personal voice, angle formulation, and subjective stance stay with the human; AI may draft, but the human finalizes.
- **Citation grounding** — Every citation must trace to a verified primary source; AI-suggested references must be confirmed before use. Use inline hyperlinks at the relevant word, not footnotes or end bibliographies. Do not fabricate URLs — if unsure, state the source name as plain text. See `references/research-toolchain.md` for the full citation style.
- **Raw material is not a cited source** — Local files the user supplies (drafts, notes, transcripts) are raw material to develop, not sources to reference in the final output. No `file://` links or "based on document X.md" references. See `references/writing-genres.md` for the full rule.

## Output format

Every long-form output from this skill must open with YAML frontmatter before the H1:

```
---
name: ${TITLE}
description: ${META_DESCRIPTION}
tag: ${TAG1}, ${TAG2}, ${TAG3}
---

# ${Title}
```

- **name** — article title, in tune with the H1 (may differ slightly if H1 is more narrative and `name` is more compact for metadata, but never different topic/focus).
- **description** — one concise sentence, SEO meta-description style, ideally 120–160 characters. State why the article is worth reading, not just restate the title. No empty filler ("This article will discuss…").
- **tag** — 3–6 relevant keywords, comma-separated, lowercase consistent. For categorization/SEO, not social hashtags. Avoid generic tags ("technology", "tips") when a more specific option exists.

This applies across all genres (blog, journal, article, news, story). Structure is the same; content adapts.

## Pre-delivery checklist

Run this concrete checklist on every draft before delivery. The stylometry parameters in the section above are the diagnostic frame; this checklist is the tactical fix.

- [ ] Opening paragraph is generic enough to be used in any article on another topic? → make it specific.
- [ ] "Not just X but Y" / "Bukan X, tapi Y" / forced rule-of-three repeating? → vary the structure.
- [ ] All sentences similar length (all short or all long)? → intersperse.
- [ ] Conclusion only restates the body? → add new implications or a follow-up question.
- [ ] Technical terms: explained once at first use, consistent throughout (do not swap terms for the same thing).
- [ ] Heading/bullet used sparingly, not to break every paragraph into a list.
- [ ] References section points to local files or `file://` links? → remove; use valid public sources or drop the section.
- [ ] YAML frontmatter (name/description/tag) present at the top of the file, before H1?
- [ ] Citations use inline hyperlinks at the relevant word, not numbered brackets or end bibliographies?
- [ ] Read aloud: does it sound like a person telling a story, or like a document assembled point by point?

## Cross-genre substitution risk

| Discipline | Human cognitive edge | AI weakness | Substitution risk |
| --- | --- | --- | --- |
| Blog | Personal voice, unique perspective, emotional engagement | Cliché, uniform styling, lack of real examples | Medium |
| Journal | Logic rigor, hypothesis formulation, methodological evaluation | No pragmatic context, reference hallucination | Low |
| Article | Cross-discipline synthesis, macro-trend meaning, logical implications | Shallow generalization, no standpoint insight | Medium |
| News | Field investigation, ethical verification, social understanding | Cannot verify primary sources directly | Very Low |
| Story | Emotional subtext, rich metaphor, complex character | Stylistic flattening, predictable plot | Very Low |
| Research | Critical evaluation, theory-gap identification, triangulation | Text processing without substantive meaning understanding | Low |

## References

- `references/writing-genres.md` — Detailed per-genre skills, workflow phases, and edge-case guidance.
- `references/research-toolchain.md` — Full 5-stage research pipeline with tool capabilities and human gates.
- `references/stylometry-checklist.md` — 5 stylometry parameters, evaluation criteria, and substitution-risk matrix.
