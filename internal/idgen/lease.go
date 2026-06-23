package idgen

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Worker-ID lease. In Kubernetes every API pod runs the same image with no
// per-pod WORKER_ID, yet each Snowflake generator needs a UNIQUE worker ID or
// two pods can mint the same code. So each pod atomically claims a free slot in
// Redis on startup and keeps it alive with a heartbeat; a dead pod's slot
// expires (TTL) and a replacement reuses it. This is the coordination-lease
// pattern (Twitter's Snowflake leased from ZooKeeper; at this scale, Redis).

const (
	workerKeyPrefix = "shortn:worker:"

	// leaseTTL is deliberately generous so a brief Redis hiccup doesn't drop the
	// lease (and risk another pod reusing the worker ID). The heartbeat renews
	// well within it — two missed renewals are tolerated before expiry.
	leaseTTL = 30 * time.Second

	// heartbeatInterval renews at ~1/3 the TTL so transient failures have margin.
	heartbeatInterval = 10 * time.Second
)

// renewScript extends the TTL only if we still own the slot — so we never extend
// a lease another pod has since taken over.
var renewScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
end
return 0`)

// releaseScript deletes the slot only if we still own it — so a graceful
// shutdown never deletes a slot another pod has taken over.
var releaseScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
end
return 0`)

// WorkerLease is a worker ID claimed from Redis and held alive by a background
// heartbeat until Release is called.
type WorkerLease struct {
	WorkerID uint16

	key   string
	owner string
	rdb   *redis.Client
	log   *slog.Logger
	stop  chan struct{}
}

// AcquireWorkerID atomically claims the lowest free worker-ID slot in
// [0, maxWorkerID], starts a heartbeat to hold it, and returns the lease. owner
// must be unique per instance (the pod name works). It errors if Redis is
// unreachable (the caller must refuse to start) or if every slot is taken.
func AcquireWorkerID(ctx context.Context, rdb *redis.Client, owner string, log *slog.Logger) (*WorkerLease, error) {
	for slot := 0; slot <= int(maxWorkerID); slot++ {
		key := workerKeyPrefix + strconv.Itoa(slot)
		ok, err := rdb.SetNX(ctx, key, owner, leaseTTL).Result()
		if err != nil {
			return nil, fmt.Errorf("acquire worker id: redis unreachable: %w", err)
		}
		if !ok {
			continue // slot held by another live pod
		}
		l := &WorkerLease{
			WorkerID: uint16(slot),
			key:      key,
			owner:    owner,
			rdb:      rdb,
			log:      log,
			stop:     make(chan struct{}),
		}
		go l.heartbeat()
		return l, nil
	}
	return nil, fmt.Errorf("acquire worker id: no free slot in [0,%d] — too many instances", maxWorkerID)
}

// heartbeat renews the lease on an interval while the pod is alive, so the slot
// never expires out from under a running generator.
func (l *WorkerLease) heartbeat() {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			res, err := renewScript.Run(ctx, l.rdb, []string{l.key}, l.owner, leaseTTL.Milliseconds()).Int()
			cancel()
			switch {
			case err != nil:
				l.log.Warn("worker-id lease renew failed; will retry", "key", l.key, "err", err)
			case res == 0:
				// TTL expired and another pod took the slot — we may now share a
				// worker ID. Loudest possible signal; the pod should be restarted.
				l.log.Error("worker-id lease LOST — restart this pod to re-acquire", "key", l.key, "worker_id", l.WorkerID)
			}
		}
	}
}

// Release stops the heartbeat and frees the slot immediately (if still owned) so
// a replacement pod can reuse it without waiting for the TTL.
func (l *WorkerLease) Release(ctx context.Context) error {
	close(l.stop)
	if err := releaseScript.Run(ctx, l.rdb, []string{l.key}, l.owner).Err(); err != nil {
		return fmt.Errorf("release worker id: %w", err)
	}
	return nil
}
