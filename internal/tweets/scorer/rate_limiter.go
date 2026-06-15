package scorer

import (
	"context"
	"sync"
	"time"
)

// RateLimiter implements token bucket algorithm for rate limiting
type RateLimiter struct {
	tokens     int           // Current tokens available
	maxTokens  int           // Maximum tokens (bucket size)
	refillRate time.Duration // How often to add a token
	lastRefill time.Time     // Last time tokens were refilled
	mu         sync.Mutex
}

// NewRateLimiter creates a rate limiter
// Example: NewRateLimiter(60, time.Minute) = 60 requests per minute
func NewRateLimiter(maxTokens int, refillInterval time.Duration) *RateLimiter {
	return &RateLimiter{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillInterval,
		lastRefill: time.Now(),
	}
}

// Wait blocks until a token is available or context is cancelled
func (rl *RateLimiter) Wait(ctx context.Context) error {
	rl.mu.Lock()

	// Refill tokens based on time passed
	now := time.Now()
	elapsed := now.Sub(rl.lastRefill)
	tokensToAdd := int(elapsed / rl.refillRate)

	if tokensToAdd > 0 {
		rl.tokens = min(rl.tokens+tokensToAdd, rl.maxTokens)
		rl.lastRefill = now
	}

	// If we have tokens, use one
	if rl.tokens > 0 {
		rl.tokens--
		rl.mu.Unlock()
		return nil
	}

	// No tokens available, unlock and wait for next refill
	waitTime := rl.refillRate - elapsed
	rl.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(waitTime):
		rl.mu.Lock()
		rl.tokens = rl.maxTokens - 1
		rl.lastRefill = time.Now()
		rl.mu.Unlock()
		return nil
	}
}
