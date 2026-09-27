-- Every audited change notifies listening servers, which push it to
-- clients subscribed to the project (live refresh).
CREATE FUNCTION tt_notify_task_event() RETURNS trigger AS $$
BEGIN
	PERFORM pg_notify('tt_task_events', json_build_object(
		'project_id', NEW.project_id, 'task_id', NEW.task_id, 'action', NEW.action)::text);
	RETURN NEW;
END
$$ LANGUAGE plpgsql;

CREATE TRIGGER task_events_notify AFTER INSERT ON task_events
	FOR EACH ROW EXECUTE FUNCTION tt_notify_task_event();
