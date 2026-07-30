CREATE INDEX IF NOT EXISTS idx_event_source_sequence
ON event(source, sequence DESC);

CREATE INDEX IF NOT EXISTS idx_event_source_topic_sequence
ON event(source, topic, sequence DESC, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_event_source_correlation_sequence
ON event(source, correlation_id, sequence DESC);
