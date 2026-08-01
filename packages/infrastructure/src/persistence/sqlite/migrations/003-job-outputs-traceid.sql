-- Add traceId column to job_outputs for run correlation.
ALTER TABLE job_outputs ADD COLUMN trace_id TEXT;
