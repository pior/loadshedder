package loadshedder

import (
	"context"
	"sync/atomic"

	"golang.org/x/sync/semaphore"
)

type Stats struct {
	Running int64
	Waiting int64
	Limit   int64
}

type Token struct {
	accepted bool
	released atomic.Bool
}

func (t *Token) Accepted() bool {
	return t.accepted
}

// Config configures a Loadshedder.
type Config struct {
	// Limit is the maximum number of concurrent requests allowed.
	// Must be positive.
	Limit int64

	// WaitingLimit is the maximum number of requests allowed to wait.
	// WaitingLimit is usually a small fraction of Limit, like 20-30%.
	// If zero, requests are rejected immediately when the concurrency limit is exceeded.
	// Must be positive.
	WaitingLimit int64
}

// Loadshedder is a framework-agnostic concurrency limiter.
// It tracks concurrent operations and determines whether new operations
// should be accepted or rejected based on the configured limits.
type Loadshedder struct {
	limit        int64
	waitingLimit int64

	current   atomic.Int64 // current number of running + waiting requests
	semaphore *semaphore.Weighted
}

// New creates a new concurrency limiter with the specified configuration.
func New(cfg Config) *Loadshedder {
	if cfg.Limit <= 0 {
		panic("loadshedder: Config.Limit must be positive")
	}
	if cfg.WaitingLimit < 0 {
		panic("loadshedder: Config.WaitingLimit cannot be negative")
	}

	return &Loadshedder{
		limit:        cfg.Limit,
		waitingLimit: cfg.WaitingLimit,
		semaphore:    semaphore.NewWeighted(cfg.Limit),
	}
}

// Acquire attempts to acquire a slot for processing.
// Always returns a Token. Check token.Accepted() to see if the request was accepted.
// Always call token.Release() when done, typically in a defer.
func (l *Loadshedder) Acquire(ctx context.Context) (Stats, *Token) {
	current := l.current.Add(1)

	if current > l.limit+l.waitingLimit {
		// Release the slot immediately (hard rejection)
		l.current.Add(-1)
		return l.stats(current), &Token{}
	}

	err := l.semaphore.Acquire(ctx, 1)
	if err != nil {
		current = l.current.Add(-1)
		return l.stats(current), &Token{}
	}

	return l.stats(current), &Token{accepted: true}
}

func (l *Loadshedder) Release(t *Token) Stats {
	if t != nil && t.accepted && t.released.CompareAndSwap(false, true) {
		l.semaphore.Release(1)
		current := l.current.Add(-1)
		return l.stats(current)
	}

	return l.stats(l.current.Load())
}

func (l *Loadshedder) Stats() Stats {
	return l.stats(l.current.Load())
}

func (l *Loadshedder) stats(current int64) Stats {
	return Stats{
		Running: min(current, l.limit),
		Waiting: max(0, current-l.limit),
		Limit:   l.limit,
	}
}
