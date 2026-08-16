# Stylometry Checklist Reference

Five stylometry parameters that distinguish human-authored text from AI-generated text, plus the cross-genre substitution-risk matrix. Load this reference when evaluating whether a draft reads as human-authored.

## The 5 parameters

### 1. Grammatical and syntactic variability
- **Human text:** Dynamic variation in sentence length and clause structure; creates a natural reading rhythm.
- **AI text:** Uniform, predictable syntactic patterns.
- **Evaluation:** Read the draft aloud. If the rhythm is monotonous or every sentence follows the same structure, revise for variability.

### 2. Semantic richness
- **Human text:** Specific metaphors rooted in cultural context and real sensory experience.
- **AI text:** Statistical word associations that surface cliché metaphors.
- **Evaluation:** Check each metaphor. If it is a common AI trope ("tapestry of…", "navigate the landscape of…", "in the realm of…"), replace with a specific, lived-experience metaphor.

### 3. Pragmatic relevance
- **Human text:** Intuitively accounts for social context, reader emotion, and local cultural implication.
- **AI text:** Operates in probability space without real pragmatic understanding.
- **Evaluation:** Does the text account for who the reader is and what they bring to the text? If it reads as context-free, revise for pragmatic grounding.

### 4. Rhetorical organization
- **Human text:** Non-linear flow that offers logical surprise yet stays coherent.
- **AI text:** Rigid adherence to standard expository schema.
- **Evaluation:** Does the argument flow in a way that a template would not produce? If the structure is "introduction → three points → conclusion" with no surprise, revise for rhetorical originality.

### 5. Style and interpersonal engagement
- **Human text:** Radiates awareness, empathy, and subjective stance.
- **AI text:** Maintains a formal, distanced, objective tone that feels cold.
- **Evaluation:** Does the writing feel like a person is present behind it? If the tone is uniformly objective and distanced, revise for interpersonal warmth.

## Cross-genre substitution-risk matrix

| Discipline | Human cognitive edge | AI weakness | Substitution risk | AI toolchain (workflow support) |
| --- | --- | --- | --- | --- |
| Blog | Personal voice, unique perspective, emotional engagement | Cliché, uniform styling, lack of real examples | Medium | Notion AI, ChatPRD (ideation & initial draft) |
| Journal | Logic rigor, hypothesis formulation, methodological evaluation | No pragmatic context, reference hallucination | Low | Paperguide, SciSpace, Scite (citation analysis) |
| Article | Cross-discipline synthesis, macro-trend meaning, logical implications | Shallow generalization, no standpoint insight | Medium | Dovetail, Storyflow (qualitative data clustering) |
| News | Field investigation, ethical verification, social understanding | Cannot verify primary sources directly | Very Low | Otter.ai, Fathom (interview transcription) |
| Story | Emotional subtext, rich metaphor, complex character | Stylistic flattening, predictable plot | Very Low | Claude (narrative-variable brainstorming) |
| Research | Critical evaluation, theory-gap identification, triangulation | Text processing without substantive meaning understanding | Low | Elicit, Consensus (scientific dissection) |

## How to use the checklist

1. Run the draft through all 5 parameters before delivery.
2. For each parameter that reads as AI-flattened, revise specifically for that parameter.
3. Re-run the revised draft through the checklist.
4. The draft is delivery-ready only when all 5 parameters read as human-authored.

The checklist is a quality gate, not a style preference. Text that fails the checklist will read as automated content to the audience, regardless of factual accuracy.

## Concrete AI-tell patterns to eliminate

The 5 parameters above are the diagnostic frame. The table below is the tactical fix — specific patterns that make text read as AI-generated, why they fail the parameters, and what to replace them with.

| Pattern | Why it fails | Replace with |
| --- | --- | --- |
| "Not just X, but Y" / "Bukan X, tapi Y" repeated every few paragraphs | Becomes a tic/formula, not genuine emphasis (fails rhetorical organization) | Vary emphasis structure, or state the claim directly without the antithesis setup |
| Forced rule-of-three ("fast, efficient, and reliable") in every list | Sounds templated, not specific observation (fails semantic richness) | Name 1-2 things most relevant and specific to the context |
| Generic opening ("In today's fast-paced world…", "Di era digital saat ini…") | Adds no information, stale (fails pragmatic relevance) | Start directly from a concrete fact, case, or dilemma |
| Conclusion that only restates body ("In conclusion, X is important because…") | No added value, feels obligatory (fails rhetorical organization) | Conclusion with new implications or a follow-up question, not a recap |
| Overuse "furthermore/moreover/di sisi lain" as paragraph bridges | Transitions become mechanical (fails rhetorical organization) | Transition through the sentence content itself — implicit reference to the prior idea |
| Heading/bullet forced onto content that flows better as prose | Over-fragmentation, loses nuance (fails syntactic variability) | Use heading/bullet only for structures that need scanning (steps, comparisons); rest is prose |
| Stacked qualifiers/hedges ("possibly", "perhaps", "tends to") in one sentence | Weakens claim without clear reason (fails interpersonal engagement) | Pick one confidence level, state it clearly |
| Em-dash or colon overused for "dramatic pause" in every sentence | Becomes a recognizable mannerism (fails syntactic variability) | Vary punctuation; many sentences need only a period |
| Metaphors from the AI default set ("tapestry of…", "navigate the landscape of…", "in the realm of…") | Statistical word associations, cliché (fails semantic richness) | Specific metaphors rooted in the subject's real world and sensory context |

## Specificity test

Generic claims without concrete detail are the easiest AI tell to recognize. Check each sentence: could the topic be swapped without changing anything? If yes, it is too generic — add numbers, names, specific examples, or contextual technical detail.

- Generic: "This technology brings many benefits to companies."
- Specific: "Latency dropped from 800ms to 220ms after the caching layer moved to the edge."

## Sentence rhythm variation

Human writing varies sentence length: short sentences for emphasis, long sentences for elaboration and nuance, interspersed. If every sentence is the same length (all short or all complex), that is a generative-text signature.

Practice: after a long sentence with a complex claim, follow with a short sentence that nails the point.

> "The latency drop is not just about infrastructure — the agent architecture that avoids excessive round-trips to external APIs also contributes heavily. Bottom line: design, not just servers."
