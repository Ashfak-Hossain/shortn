-- The Table where each click is recorded.
CREATE TABLE click_events (
    event_id   TEXT PRIMARY KEY, -- the dedup key 
    code       TEXT NOT NULL, -- The short code that was clicked
    ts         TIMESTAMPTZ NOT NULL,  -- Timestamp of the click event
    referrer   TEXT NOT NULL DEFAULT '', -- Referrer URL, if available
    user_agent TEXT NOT NULL DEFAULT '' -- User agent string, if available
);

-- Index on the code column for efficient querying of click events by short code.
CREATE INDEX idx_click_events_code ON click_events (code);

-- 
/**
  * The Table track Kafka consumer offsets for exactly-once processing.
  * This allows the click analytics to resume from the last committed offset
  * in case of restarts, ensuring no click events are missed or double-counted.
 **/
CREATE TABLE kafka_offsets (
    consumer_group TEXT    NOT NULL, -- Name of the Kafka consumer group
    topic          TEXT    NOT NULL, -- The Kafka topic
    partition      INTEGER NOT NULL, -- The partition number
    "offset"       BIGINT  NOT NULL, -- The committed offset
    PRIMARY KEY (consumer_group, topic, partition)
);