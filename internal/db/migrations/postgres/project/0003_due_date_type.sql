-- due_date becomes a real DATE (it was TEXT with a format check).
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_due_date_check;
ALTER TABLE tasks ALTER COLUMN due_date TYPE DATE USING due_date::date;
