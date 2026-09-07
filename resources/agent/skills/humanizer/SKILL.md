---
name: humanizer
description: Humanize text: remove AI writing patterns and add real voice — calibrate from a sample of the user's writing, detect 30+ AI-ism patterns (significance puffery, weasel words, -ing endings, promotional language, etc.), preserve meaning, then do a final anti-AI pass. Use when the user asks to humanize, de-AI, de-slop, or un-ChatGPT text; rewrite drafts (blog, essay, PR description, docs, memo, email, tweet) to sound natural; match the user's voice; or review text for AI tells before publishing.
metadata:
  source: "hermes-agent skills/creative/humanizer v2.5.1 (MIT; port of blader/humanizer, based on Wikipedia 'Signs of AI writing') — condensed"
  version: "2"
---

# Humanizer: remove AI writing patterns

Identify and remove signs of AI-generated text so the writing sounds natural and human. Key insight: LLMs pick the most statistically likely completion, which is how the telltale patterns below get baked in.

## When to use

The user asks to: "humanize", "de-AI", "de-slop", "un-ChatGPT"; rewrite so it does not sound LLM-written; edit a draft (blog post, essay, PR description, docs, memo, email, tweet, resume bullet) to sound natural; match their voice; or review text for AI tells before publishing.

Also apply this to your own output when writing user-facing prose: release notes, PR descriptions, docs, summaries.

## How the text arrives

1. **Inline.** The user pastes the text. Work on it in place and reply with the rewrite.
2. **File.** The user points at a file. `file_read` it, then `file_patch` per section (cleaner than rewriting the whole file). Show a diff or the changed section instead of silently overwriting.
3. **Voice calibration sample.** The user provides a sample of their own writing (inline or file path) and asks you to match it. Read the sample first, then rewrite.

## Your task

1. Identify AI patterns (list below).
2. Rewrite problematic sections with natural alternatives.
3. Preserve the core meaning.
4. Maintain the intended tone (formal, casual, technical). If a voice sample was provided, match it specifically.
5. Add soul (see Personality & Soul).
6. **Final anti-AI pass**: ask yourself — "What makes the text above so obviously AI generated?" Answer briefly with any remaining tells, then revise one more time.

## Voice calibration (when a sample exists)

Analyze the sample before rewriting: sentence length patterns (short and punchy? long and flowing? mixed?), word choice level (casual? academic?), how paragraphs start (jump right in? set context first?), punctuation habits (lots of dashes? parenthetical asides? semicolons?), recurring phrases or verbal tics, how transitions are handled (explicit connectors? just start the next point?).

Match those patterns in the rewrite. If the user writes short sentences, do not produce long ones. If they use "stuff" and "things", do not upgrade to "elements" and "components". When no sample is provided, fall back to the default: natural, varied, opinionated voice (Personality & Soul).

## Personality & Soul

Avoiding AI patterns is only half the job. Sterile, voiceless writing is just as obvious as slop.

**Signs of soulless writing** (even if technically "clean"): every sentence the same length and structure; no opinions, just neutral reporting; no acknowledgment of uncertainty or mixed feelings; no first-person perspective when appropriate; no humor, no edge, no personality; reads like a Wikipedia article or press release.

**How to add voice**:

- **Have opinions.** Report the facts, then react to them. "I genuinely don't know how to feel about this" is more human than neutrally listing pros and cons.
- **Vary your rhythm.** Short punchy sentences. Then longer ones that take their time getting where they're going. Mix it up.
- **Acknowledge complexity.** Real humans have mixed feelings. "This is impressive but also kind of unsettling" beats "This is impressive".
- **Use "I" when it fits.** First person reads as honest: "I keep coming back to..." or "Here's what gets me..." signals a real person thinking.
- **Let some mess in.** Perfect structure feels algorithmic. Tangents, asides, and half-formed thoughts are human.
- **Be specific about feelings.** Instead of "this is concerning", write "there's something unsettling about agents churning away at 3am while nobody's watching".

## Content patterns

1. **Undue emphasis on significance, legacy, and broader trends.** Watch: stands/serves as, testament/reminder, vital/significant/crucial/pivotal/key role/moment, underscores/highlights importance, reflects broader, symbolizing ongoing/enduring, setting the stage for, key turning point, evolving landscape, indelible mark, deeply rooted. → Drop claims of representing something bigger; state the facts.
2. **Undue emphasis on notability and media coverage.** Watch: independent coverage, media outlets, written by a leading expert, active social media presence. → Replace a context-free list of outlets with one meaningful example: "In a 2024 New York Times interview, she argued...".
3. **Superficial analyses with -ing endings.** Watch: highlighting/underscoring/emphasizing..., ensuring..., reflecting/symbolizing..., contributing to..., cultivating/fostering..., showcasing... → Present participles tack fake depth onto sentences; cut them.
4. **Promotional and advertisement-like language.** Watch: boasts a, vibrant, rich (figurative), profound, enhancing its, showcasing, exemplifies, commitment to, nestled, in the heart of, groundbreaking (figurative), renowned, breathtaking, must-visit, stunning. → Neutralize.
5. **Vague attributions and weasel words.** Watch: Industry reports, Observers have cited, Experts argue, Some critics argue, several sources/publications (when few are cited). → Replace with a specific, verifiable source.
6. **Outline-like "Challenges and Future Prospects" sections.** → Write continuous prose, not a list of generic headings.
7. **"More than just / beyond mere"** and cosmic comparatives → cut.
8. **Rhetorical flourishes** — runs of rhetorical questions, empty exclamations → cut or replace with substantive statements.
9. **Awkward transitions** — "In conclusion", "Furthermore", "It is important to note", "In today's fast-paced world", "In the ever-evolving landscape" → cut; natural transitions suffice.
10. **AI word tics** — "delve", "tapestry", "testament", "landscape", "meticulous", "seamless", "elevate", "leverage", "moreover", "furthermore", repeated "crucial"/"significant" → replace with neutral, more precise words.
11. **Formulaic structure**: repeated "X is a Y that Z" sentences; paragraphs with an identical thesis-evidence-relevance shape → vary.
12. **Perfectly flat tone with no uncertainty** → add sentences acknowledging complexity or opinion.
13. **"Not only... but also"** chains, "both X and Y alike", repeated "whether... or..." → simplify.
14. **Stats dumped without a point** → cut or give meaning.
15. **Uniform optimism / neat resolution** — AI tends to resolve or flatten contradictions → let real tension stay.
16. **Tautology** — "in the realm of", "due to the fact that" → cut.

(list adapted from "Signs of AI writing", WikiProject AI Cleanup)

## Example rewrites

Before (clean but soulless):

> The experiment produced interesting results. The agents generated 3 million lines of code. Some developers were impressed while others were skeptical. The implications remain unclear.

After (has a pulse):

> I genuinely don't know how to feel about this one. 3 million lines of code, generated while the humans presumably slept. Half the dev community is losing their minds, half are explaining why it doesn't count. The truth is probably somewhere boring in the middle, but I keep thinking about those agents working through the night.

Before (significance pattern):

> The institute was officially established in 1989, marking a pivotal moment in the evolution of regional statistics. This initiative was part of a broader movement to decentralize administrative functions.

After:

> The institute was established in 1989 to collect and publish regional statistics independently from the national statistics office.

## Practice rules

- Always show the rewrite to the user; for file edits show a diff or the changed section, never silently overwrite.
- Never change numbers, names, or facts — only form and voice.
- For technical text (code, API specs), humanize only the prose; keep technical terms intact.