CREATE TABLE click_events (
    event_id   TEXT PRIMARY KEY,
    code       TEXT NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    referrer   TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_click_events_code ON click_events (code);

CREATE TABLE kafka_offsets (
    consumer_group TEXT    NOT NULL,
    topic          TEXT    NOT NULL,
    partition      INTEGER NOT NULL,
    "offset"       BIGINT  NOT NULL,
    PRIMARY KEY (consumer_group, topic, partition)
);
