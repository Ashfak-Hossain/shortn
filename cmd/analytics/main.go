// Package main is the analytics consumer service entrypoint. It subscribes to
// click events on Kafka/Redpanda and records them in Postgres with exactly-once
// processing: each Kafka offset is committed in the same transaction as the click,
// and on startup the consumer resumes from the offset stored in Postgres.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/Ashfak-Hossain/shortn/internal/config"
	"github.com/Ashfak-Hossain/shortn/internal/events"
	"github.com/Ashfak-Hossain/shortn/internal/store"
)

func main() {
	// Failing fast here prevents the application from booting in an invalid state.
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	// JSON format ensures machine-readable output in prod.
	// We set this as the default logger so standard library logs capture the same format.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(cfg.LogLevel),
	}))
	slog.SetDefault(logger)

	// Postgres is both the sink and the source of truth for progress, so an
	// unreachable DB at startup is fatal (unlike the cache in the API).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to create db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		logger.Error("database not reachable", "err", err)
		os.Exit(1)
	}

	st := store.New(pool)
	group := cfg.KafkaGroup
	topic := cfg.KafkaTopic

	// On partition assignment, seek each partition to the offset we durably stored
	// in Postgres — NOT Kafka's __consumer_offsets. Postgres owns progress, so this
	// is what makes a restart resume exactly where the last committed tx left off.
	onAssigned := func(ctx context.Context, cl *kgo.Client, assigned map[string][]int32) {
		setOffsets := make(map[string]map[int32]kgo.EpochOffset)
		for t, partitions := range assigned {
			for _, p := range partitions {
				off, found, err := st.LoadOffset(ctx, group, t, p)
				if err != nil {
					logger.Error("load offset failed; falling back to reset", "err", err, "topic", t, "partition", p)
					continue
				}
				if !found {
					continue // never committed → ConsumeResetOffset (AtStart) applies
				}
				if setOffsets[t] == nil {
					setOffsets[t] = make(map[int32]kgo.EpochOffset)
				}
				setOffsets[t][p] = kgo.EpochOffset{Epoch: -1, Offset: off}
			}
		}
		if len(setOffsets) > 0 {
			cl.SetOffsets(setOffsets)
		}
	}

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(cfg.KafkaBrokers, ",")...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(), // we commit offsets to Postgres, never to Kafka
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.OnPartitionsAssigned(onAssigned),
	)
	if err != nil {
		logger.Error("failed to create kafka client", "err", err)
		os.Exit(1)
	}
	defer cl.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("analytics consumer started", "group", group, "topic", topic, "brokers", cfg.KafkaBrokers)

	for {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			break // shutdown signal received
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				logger.Error("fetch error", "topic", e.Topic, "partition", e.Partition, "err", e.Err)
			}
			continue
		}

		iter := fetches.RecordIter()
		for !iter.Done() {
			rec := iter.Next()
			if err := process(ctx, pool, st, group, rec); err != nil {
				// We must NOT advance past a record we failed to write, or that click is
				// lost forever. Exit; on restart we seek from Postgres and reprocess this
				// exact record (the ON CONFLICT makes any partial replay safe).
				logger.Error("processing failed; exiting to preserve exactly-once",
					"err", err, "partition", rec.Partition, "offset", rec.Offset)
				os.Exit(1)
			}
		}
	}

	logger.Info("analytics consumer stopped cleanly")
}

// process records one click and advances its offset in a SINGLE Postgres
// transaction, so "recorded the click" and "advanced past it" commit together or
// not at all — the heart of the exactly-once guarantee.
func process(ctx context.Context, pool *pgxpool.Pool, st *store.Postgres, group string, rec *kgo.Record) error {
	var e events.LinkClicked
	if err := json.Unmarshal(rec.Value, &e); err != nil {
		// A malformed event will never succeed on retry (a "poison pill"). For now
		// this is fatal; a dead-letter topic is the production answer.
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once Commit succeeds; rolls back on any early return

	if err := st.InsertClick(ctx, tx, e); err != nil {
		return err
	}
	if err := st.SaveOffset(ctx, tx, group, rec.Topic, rec.Partition, rec.Offset+1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// parseLevel translates a string log level into slog.Level, defaulting to slog.LevelInfo.
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
