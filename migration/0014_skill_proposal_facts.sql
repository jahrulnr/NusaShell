-- Two facts a proposal keeps for the owner's queue and the candidate's refusals.
-- decided_open is whether the proposal was still open when the owner closed it.
-- Closing one that had already gone stale only clears it from the queue, so
-- such a row holds no name and no refusal. Rows written before this file read
-- as open, which is what their refusals meant when the owner gave them.
ALTER TABLE skill_proposals ADD COLUMN decided_open INTEGER NOT NULL DEFAULT 1;
-- When the cited evidence happened, in unix milliseconds; 0 when the turn
-- carried no time.
ALTER TABLE skill_proposals ADD COLUMN evidence_at INTEGER NOT NULL DEFAULT 0;
