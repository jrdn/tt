-- Add 'blocked' to the allowed task statuses.
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check
	CHECK (status IN ('backlog','open','ready','in_progress','blocked','in_review','done','cancelled'));
