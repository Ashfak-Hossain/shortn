package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Ashfak-Hossain/shortn/internal/events"
	"github.com/Ashfak-Hossain/shortn/internal/shortener"
)

// InsertClick records a single click event within the caller's transaction.
// The ON CONFLICT (event_id) DO NOTHING clause makes a redelivered event a
// harmless no-op, so the consumer is safe to reprocess after a crash.
func (p *Postgres) InsertClick(ctx context.Context, tx pgx.Tx, e events.LinkClicked) error {
	const query = `
		INSERT INTO click_events (event_id, code, ts, referrer, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (event_id) DO NOTHING`
	_, err := tx.Exec(ctx, query, e.EventID, e.Code, e.Timestamp, e.Referrer, e.UserAgent)
	return err
}

// SaveOffset upserts the consumer group's committed offset for one partition
// within the caller's transaction. Writing it in the same transaction as the
// click is what makes "processed" and "advanced past it" a single atomic fact.
func (p *Postgres) SaveOffset(ctx context.Context, tx pgx.Tx, group, topic string, partition int32, offset int64) error {
	const query = `
		INSERT INTO kafka_offsets (consumer_group, topic, partition, "offset")
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer_group, topic, partition)
		DO UPDATE SET "offset" = EXCLUDED."offset"`
	_, err := tx.Exec(ctx, query, group, topic, partition, offset)
	return err
}

// LoadOffset returns the last committed offset for a partition and whether one
// exists. On a partition this group has never committed, found is false and the
// caller should start from the earliest offset. It reads outside any transaction
// because it runs once, on partition assignment.
func (p *Postgres) LoadOffset(ctx context.Context, group, topic string, partition int32) (offset int64, found bool, err error) {
	const query = `SELECT "offset" FROM kafka_offsets WHERE consumer_group = $1 AND topic = $2 AND partition = $3`
	err = p.pool.QueryRow(ctx, query, group, topic, partition).Scan(&offset)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return offset, true, nil
}

// GetStats returns the total click count and an hourly time series for a code.
func (p *Postgres) GetStats(ctx context.Context, code string) (shortener.Stats, error) {
	const query = `
		SELECT date_trunc('hour', ts) AS bucket, count(*) AS c
		FROM click_events
		WHERE code = $1
		GROUP BY bucket
		ORDER BY bucket`
	rows, err := p.pool.Query(ctx, query, code)
	if err != nil {
		return shortener.Stats{}, err
	}
	defer rows.Close()

	stats := shortener.Stats{Code: code}
	for rows.Next() {
		var b shortener.ClickBucket
		if err := rows.Scan(&b.Bucket, &b.Count); err != nil {
			return shortener.Stats{}, err
		}
		stats.Total += b.Count
		stats.Series = append(stats.Series, b)
	}
	return stats, rows.Err()
}
