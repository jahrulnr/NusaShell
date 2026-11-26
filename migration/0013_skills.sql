-- Learned-skill proposals waiting for the owner's decision, and the ownership
-- row of each learned skill. See docs/skills.md. A proposal's evidence and
-- dismissal reason are the owner's words; the ownership row is the loop's only
-- write authority over the nusashell skill root — SKILL.md stays portable
-- with only name and description in its frontmatter, so provenance lives here.
CREATE TABLE skill_proposals (
    id             TEXT    PRIMARY KEY,
    op             TEXT    NOT NULL,
    kind           TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    description    TEXT    NOT NULL DEFAULT '',
    body           TEXT    NOT NULL DEFAULT '',
    reason         TEXT    NOT NULL DEFAULT '',
    evidence       TEXT    NOT NULL,
    evidence_kind  TEXT    NOT NULL,
    -- The tool a call-kind evidence cites; empty for the other kinds.
    tool           TEXT    NOT NULL DEFAULT '',
    target_name    TEXT    NOT NULL DEFAULT '',
    target_digest  TEXT    NOT NULL DEFAULT '',
    target_level   TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL,
    accepted_path  TEXT    NOT NULL DEFAULT '',
    accepted_level TEXT    NOT NULL DEFAULT '',
    dismiss_reason TEXT    NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    decided_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX skill_proposals_status ON skill_proposals (status, created_at);
-- One pending proposal per skill name: two open rows for one class would
-- decide against each other in the owner's queue, so the database — not the
-- caller's discipline — enforces it.
CREATE UNIQUE INDEX skill_proposals_one_pending ON skill_proposals (name) WHERE status = 'pending';
-- The candidate's cooldown asks for one name: anything pending, or decided recently.
CREATE INDEX skill_proposals_name ON skill_proposals (name, status);

-- One row per learned skill, keyed by root and name so a same-named skill of
-- another root never answers for it. digest covers the whole rendered SKILL.md
-- the loop is authorized to rewrite — frontmatter included, because that is
-- what a read hashes — not the last disk state observed: an owner's edit flips
-- the level to user and moves nothing.
CREATE TABLE skill_ownership (
    root   TEXT NOT NULL,
    name   TEXT NOT NULL,
    level  TEXT NOT NULL,
    digest TEXT NOT NULL,
    PRIMARY KEY (root, name)
);
