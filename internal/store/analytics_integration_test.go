//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/Ashfak-Hossain/shortn/internal/events"
)

// TestIntegration_InsertClick_Idempotent proves the consumer's exactly-once safety net
// against a real Postgres: inserting the same event_id twice records exactly one click,
// because click_events.event_id is the primary key and InsertClick uses
// ON CONFLICT (event_id) DO NOTHING. This is T3 from the verification log, automated.
func TestIntegration_InsertClick_Idempotent(t *testing.T) {
	ctx := context.Background()
	st := New(startPostgres(t))

	evt := events.LinkClicked{
		EventID:   "evt-dup-1",
		Code:      "dupcode",
		Timestamp: time.Now().UTC(),
		Referrer:  "https://ref.example/",
		UserAgent: "test-agent",
		Version:   1,
	}

	// Insert the SAME event twice, each in its own committed transaction — exactly
	// what a broker redelivery looks like to the consumer.
	for i := 0; i < 2; i++ {
		tx, err := st.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin tx %d: %v", i, err)
		}
		if err := st.InsertClick(ctx, tx, evt); err != nil {
			t.Fatalf("InsertClick %d: %v", i, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}

	// Exactly one row must exist for that event_id, no matter how many times it was delivered.
	var count int
	if err := st.pool.QueryRow(ctx,
		"SELECT count(*) FROM click_events WHERE event_id = $1", evt.EventID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("click_events rows for event_id=%q = %d, want 1 (duplicate must dedup)", evt.EventID, count)
	}
}
