package inmem

import (
	"fmt"
	"sync"
	"time"

	"github.com/your_github_user_or_org/zapalert/backend"
	"github.com/your_github_user_or_org/zapalert/internal/level"
)

// Config controls rolling-window behavior for the in-memory backend.
type Config struct {
	Window      time.Duration
	BucketCount int
	MethodTTL   time.Duration
}

type bucket struct {
	alerts int
	total  int
	fail   int
}

type methodState struct {
	buckets     []bucket
	bucketIDs   []int64
	lastBucket  int64
	initialized bool
	lastSeen    time.Time
}

// Backend is an in-memory rolling-window implementation of backend.Backend.
type Backend struct {
	mu         sync.Mutex
	states     map[string]*methodState
	window     time.Duration
	bucketSize time.Duration
	bucketCnt  int
	methodTTL  time.Duration
	nextEvict  time.Time
}

var _ backend.Backend = (*Backend)(nil)

// New creates an in-memory backend.
func New(cfg Config) (*Backend, error) {
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("window must be > 0")
	}
	if cfg.BucketCount <= 0 {
		return nil, fmt.Errorf("bucket count must be > 0")
	}
	bucketSize := cfg.Window / time.Duration(cfg.BucketCount)
	if bucketSize <= 0 {
		return nil, fmt.Errorf("bucket size must be > 0; increase window or reduce bucket count")
	}
	methodTTL := cfg.MethodTTL
	if methodTTL <= 0 {
		methodTTL = 2 * cfg.Window
	}

	return &Backend{
		states:     make(map[string]*methodState),
		window:     cfg.Window,
		bucketSize: bucketSize,
		bucketCnt:  cfg.BucketCount,
		methodTTL:  methodTTL,
	}, nil
}

// IncrAlert increments the alert counter for a method.
func (b *Backend) IncrAlert(method string, _ level.AlertLevel, at time.Time) error {
	if method == "" {
		return fmt.Errorf("method must not be empty")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.evictLocked(at)
	state := b.methodLocked(method, at)
	current := b.bucketFor(at)
	b.rotateLocked(state, current)

	idx := b.index(current)
	state.buckets[idx].alerts++
	state.lastSeen = at
	return nil
}

// IncrRequest increments request totals (and failures if success=false) for a method.
func (b *Backend) IncrRequest(method string, success bool, at time.Time) error {
	if method == "" {
		return fmt.Errorf("method must not be empty")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.evictLocked(at)
	state := b.methodLocked(method, at)
	current := b.bucketFor(at)
	b.rotateLocked(state, current)

	idx := b.index(current)
	state.buckets[idx].total++
	if !success {
		state.buckets[idx].fail++
	}
	state.lastSeen = at
	return nil
}

// Snapshot returns rolling-window metrics for a method.
func (b *Backend) Snapshot(method string, at time.Time) (backend.Metrics, error) {
	if method == "" {
		return backend.Metrics{}, fmt.Errorf("method must not be empty")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.evictLocked(at)
	state, ok := b.states[method]
	if !ok {
		return backend.Metrics{}, nil
	}
	current := b.bucketFor(at)
	b.rotateLocked(state, current)
	state.lastSeen = at

	metrics := backend.Metrics{}
	for i := range state.buckets {
		id := state.bucketIDs[i]
		if id < 0 {
			continue
		}
		if current-id >= int64(b.bucketCnt) {
			continue
		}
		metrics.AlertCount += state.buckets[i].alerts
		metrics.RequestTotal += state.buckets[i].total
		metrics.RequestFailures += state.buckets[i].fail
	}
	if metrics.RequestTotal > 0 {
		metrics.FailureRate = float64(metrics.RequestFailures) / float64(metrics.RequestTotal)
	}
	return metrics, nil
}

func (b *Backend) methodLocked(method string, now time.Time) *methodState {
	state, ok := b.states[method]
	if ok {
		return state
	}

	bucketIDs := make([]int64, b.bucketCnt)
	for i := range bucketIDs {
		bucketIDs[i] = -1
	}
	state = &methodState{
		buckets:   make([]bucket, b.bucketCnt),
		bucketIDs: bucketIDs,
		lastSeen:  now,
	}
	b.states[method] = state
	return state
}

func (b *Backend) rotateLocked(state *methodState, current int64) {
	if !state.initialized {
		state.initialized = true
		state.lastBucket = current
		idx := b.index(current)
		state.bucketIDs[idx] = current
		state.buckets[idx] = bucket{}
		return
	}

	steps := current - state.lastBucket
	if steps > 0 {
		if steps >= int64(b.bucketCnt) {
			for i := range state.buckets {
				state.buckets[i] = bucket{}
				state.bucketIDs[i] = -1
			}
		} else {
			for i := int64(1); i <= steps; i++ {
				idx := b.index(state.lastBucket + i)
				state.buckets[idx] = bucket{}
				state.bucketIDs[idx] = -1
			}
		}
		state.lastBucket = current
	}

	idx := b.index(current)
	if state.bucketIDs[idx] != current {
		state.buckets[idx] = bucket{}
		state.bucketIDs[idx] = current
	}
}

func (b *Backend) evictLocked(now time.Time) {
	if b.methodTTL <= 0 {
		return
	}
	if !b.nextEvict.IsZero() && now.Before(b.nextEvict) {
		return
	}
	for method, state := range b.states {
		if now.Sub(state.lastSeen) > b.methodTTL {
			delete(b.states, method)
		}
	}
	b.nextEvict = now.Add(b.bucketSize)
}

func (b *Backend) bucketFor(at time.Time) int64 {
	return at.UnixNano() / b.bucketSize.Nanoseconds()
}

func (b *Backend) index(bucket int64) int {
	idx := bucket % int64(b.bucketCnt)
	if idx < 0 {
		idx += int64(b.bucketCnt)
	}
	return int(idx)
}
