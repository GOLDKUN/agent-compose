-- Declarative cron triggers historically materialized the implicit timezone as
-- UTC. Their authoring spec could not explicitly select UTC, so these rows can
-- safely adopt the new daemon-local default. Script schedulers are excluded
-- because their historical UTC value may have been explicit.
UPDATE scheduler_trigger
SET spec_json = json_set(spec_json, '$.timezone', 'Local'),
    next_fire_at = 0
WHERE kind = 'cron'
  AND json_extract(spec_json, '$.timezone') = 'UTC'
  AND scheduler_id IN (
      SELECT id
      FROM project_scheduler
      WHERE json_type(spec_json, '$.triggers') = 'array'
        AND json_array_length(spec_json, '$.triggers') > 0
  );
