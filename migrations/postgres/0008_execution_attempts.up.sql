-- Attempts and recovery of an execution. attempts counts the claims that started
-- a run; last_recovered_at is set when a reclaim took the run back from a dead
-- worker; run_job_id and run_job_attempt identify the job claim that holds the
-- run while it is running (empty and 0 when none), so a stale claim cannot
-- reset a run that a newer claim drives.
ALTER TABLE executions ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE executions ADD COLUMN last_recovered_at TIMESTAMPTZ;
ALTER TABLE executions ADD COLUMN run_job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE executions ADD COLUMN run_job_attempt INTEGER NOT NULL DEFAULT 0;
