// Package idgen produces coordination-free, distributed unique IDs for the shortn service.
//
// The primary implementation is [SnowflakeGenerator], which encodes a millisecond
// timestamp, a worker ID, and a per-millisecond sequence counter into a compact
// base-62 string. All implementations are safe for concurrent use by multiple goroutines.
package idgen

import (
	"fmt"
	"sync"
	"time"

	"github.com/Ashfak-Hossain/shortn/internal/shortener"
	sqids "github.com/sqids/sqids-go"
)

const (
	// epoch is the project's custom epoch in Unix milliseconds, chosen to keep
	// the timestamp component small and the generated codes short.
	epoch        int64  = 954028800000
	workerBits          = 10
	sequenceBits        = 12
	maxWorkerID  uint16 = (1 << workerBits) - 1   // 1023
	maxSequence  uint16 = (1 << sequenceBits) - 1 // 4095
)

// ErrClockRegressed is returned by [SnowflakeGenerator.Generate] when the system
// clock has moved backward, which would risk issuing a duplicate ID.
var ErrClockRegressed = fmt.Errorf("clock moved backwards; refusing to generate ID")

// SnowflakeGenerator is a coordination-free, distributed ID generator that
// produces short, non-sequential, URL-safe codes. It is safe for concurrent use
// by multiple goroutines.
type SnowflakeGenerator struct {
	mu       sync.Mutex
	workerID uint16
	sequence uint16
	lastMs   int64
	sq       *sqids.Sqids
}

// NewSnowflakeGenerator returns a [SnowflakeGenerator] configured with the given
// workerID. workerID must be in the range [0, 1023]; values outside that range
// return a non-nil error.
func NewSnowflakeGenerator(workerID uint16, sq *sqids.Sqids) (*SnowflakeGenerator, error) {
	if workerID > maxWorkerID {
		return nil, fmt.Errorf("workerID must be in range [0, 1023]")
	}
	return &SnowflakeGenerator{workerID: workerID, sq: sq}, nil
}

// Compile-time check that *SnowflakeGenerator satisfies shortener.IDGenerator.
var _ shortener.IDGenerator = (*SnowflakeGenerator)(nil)

// Generate returns a unique, non-sequential code derived from a
// Snowflake ID. It returns [ErrClockRegressed] if the system clock has moved
// backward since the last call.
func (g *SnowflakeGenerator) Generate() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Milliseconds since our custom epoch. Subtracting the epoch keeps
	// the number small so the final encoded string stays short.
	now := time.Now().UnixMilli() - epoch

	// Clock went backward (NTP correction, VM migration). Emitting an ID here
	// could collide with one already issued at this timestamp, so we refuse.
	if now < g.lastMs {
		return "", ErrClockRegressed
	}

	if now == g.lastMs {
		// Still in the same millisecond — increment the sequence counter to
		// differentiate this ID from the previous one issued at the same ms.
		g.sequence++
		if g.sequence > maxSequence {
			// All 4096 sequence slots for this ms are exhausted. Wait for the
			// clock to tick forward before issuing another ID.
			time.Sleep(time.Millisecond)
			now = time.Now().UnixMilli() - epoch
			g.sequence = 0
			g.lastMs = now
		}
	} else {
		// New millisecond — reset the sequence and record the new timestamp.
		g.sequence = 0
		g.lastMs = now
	}

	// Pack the three fields into one 64-bit integer:
	//   [ timestamp: 41 bits ][ workerID: 10 bits ][ sequence: 12 bits ]
	// Left-shifting pushes each value into its designated slot; OR merges them.
	id := (uint64(now) << (workerBits + sequenceBits)) | (uint64(g.workerID) << sequenceBits) | uint64(g.sequence)
	code, err := g.sq.Encode([]uint64{id})
	if err != nil {
		return "", fmt.Errorf("encode id: %w", err)
	}
	return code, nil

}
