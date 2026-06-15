// Package events defines the LinkClicked event — the contract shared between the
// API (producer) and the analytics consumer — plus a Kafka-backed publisher.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// LinkClicked is emitted every time a short link is resolved. It is the wire
// contract between cmd/api (producer) and cmd/analytics (consumer): both import
// this struct so they agree on field names, types, and JSON shape.
type LinkClicked struct {
	EventID   string    `json:"event_id"`
	Code      string    `json:"code"`
	Timestamp time.Time `json:"ts"`
	Referrer  string    `json:"referrer"`
	UserAgent string    `json:"user_agent"`
	Version   int       `json:"version"`
}

// KafkaPublisher publishes LinkClicked events to a Kafka/Redpanda topic.
// It is safe for concurrent use by multiple goroutines.
type KafkaPublisher struct {
	client *kgo.Client
}

// NewKafkaPublisher returns a KafkaPublisher that produces to topic on the given
// Kafka/Redpanda brokers. It returns an error if the underlying client cannot be created.
func NewKafkaPublisher(brokers []string, topic string) (*KafkaPublisher, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.DefaultProduceTopic(topic),
		// Bound how long a background publish keeps retrying when Redpanda is
		// unreachable, so click-publish goroutines drain instead of piling up
		// during an outage. Analytics is best-effort — a dropped click is fine.
		kgo.RecordDeliveryTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("creating kafka client: %w", err)
	}
	return &KafkaPublisher{client: client}, nil
}

// Publish serializes the event to JSON and produces it to the topic, keyed by
// Code so all clicks for one link land on the same partition (preserving per-link
// order). It blocks until the broker acknowledges the record and returns any
// produce error; the caller treats a failure as non-fatal.
func (p *KafkaPublisher) Publish(ctx context.Context, e LinkClicked) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshaling LinkClicked: %w", err)
	}
	rec := &kgo.Record{Key: []byte(e.Code), Value: payload}
	return p.client.ProduceSync(ctx, rec).FirstErr()
}

// Close flushes any buffered records and closes the underlying client. Call it
// on shutdown so an in-flight event is not lost.
func (p *KafkaPublisher) Close() {
	p.client.Close()
}
