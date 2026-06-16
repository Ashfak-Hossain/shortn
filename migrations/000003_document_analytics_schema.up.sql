-- Document the analytics schema in the database catalog (queryable via psql \d+),
-- rather than editing the already-applied 000002 migration. These are metadata
-- comments only — they change no data or structure.

COMMENT ON TABLE click_events IS 'One row per click — the source of truth for analytics.';
COMMENT ON COLUMN click_events.event_id IS 'Dedup key (producer UUID); the primary key that makes reprocessing idempotent.';
COMMENT ON COLUMN click_events.code IS 'The short code that was clicked.';
COMMENT ON COLUMN click_events.ts IS 'When the click happened (UTC).';
COMMENT ON COLUMN click_events.referrer IS 'Referrer URL, if the browser sent one.';
COMMENT ON COLUMN click_events.user_agent IS 'User-agent string, if available.';

COMMENT ON TABLE kafka_offsets IS 'Per-partition committed Kafka offset for exactly-once processing; the consumer resumes from here on restart.';
COMMENT ON COLUMN kafka_offsets.consumer_group IS 'Kafka consumer group name.';
COMMENT ON COLUMN kafka_offsets.topic IS 'Kafka topic.';
COMMENT ON COLUMN kafka_offsets.partition IS 'Partition number within the topic.';
COMMENT ON COLUMN kafka_offsets."offset" IS 'Next offset to consume; committed in the same transaction as the click.';
