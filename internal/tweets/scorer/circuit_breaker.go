package scorer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// CircuitBreaker implements the circuit breaker pattern
// Prevents cascading failures by stopping calls when API is failing
type CircuitBreaker struct {
	failureThreshold int           // Open circuit after N failures
	successThreshold int           // Close circuit after N successes
	timeout          time.Duration  // How long to stay open
	halfOpenTimeout  time.Duration // How long to stay in half-open

	failures    int           // Current failure count
	successes   int           // Current success count (in half-open)
	state       State         // Current state
	lastFailure time.Time     // When circuit last opened
	mu          sync.RWMutex
}

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// NewCircuitBreaker creates a circuit breaker
func NewCircuitBreaker(failureThreshold, successThreshold int, timeout, halfOpenTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
		halfOpenTimeout:  halfOpenTimeout,
		state:            StateClosed,
	}
}

// Call executes a function with circuit breaker protection
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error {
	// Check if we can make the call
	if err := cb.beforeCall(); err != nil {
		return err
	}

	// Execute the function
	err := fn()

	// Update circuit state based on result
	cb.afterCall(err, false)
	return err
}

// CallWithCriticalError executes a function and can immediately open circuit on critical errors
// Use this when you detect systematic errors (401, 404) that should open circuit immediately
func (cb *CircuitBreaker) CallWithCriticalError(ctx context.Context, fn func() error, isCriticalError func(error) bool) error {
	// Check if we can make the call
	if err := cb.beforeCall(); err != nil {
		return err
	}

	// Execute the function
	err := fn()

	// Check if this is a critical error that should open circuit immediately
	isCritical := err != nil && isCriticalError != nil && isCriticalError(err)

	// Update circuit state based on result
	cb.afterCall(err, isCritical)
	return err
}

func (cb *CircuitBreaker) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		// Normal operation, allow call
		return nil

	case StateOpen:
		// Check if timeout has passed
		if now.Sub(cb.lastFailure) >= cb.timeout {
			// Move to half-open state
			cb.state = StateHalfOpen
			cb.successes = 0
			return nil
		}
		// Still open, reject call
		return ErrCircuitOpen

	case StateHalfOpen:
		// Allow limited calls to test if service recovered
		return nil
	}

	return nil
}

func (cb *CircuitBreaker) afterCall(err error, isCritical bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure(isCritical)
	} else {
		cb.onSuccess()
	}
}

func (cb *CircuitBreaker) onFailure(isCritical bool) {
	switch cb.state {
	case StateClosed:
		if isCritical {
			// Critical error (401, 404, etc.) - open circuit immediately
			// This prevents wasting API calls when we know it will fail
			cb.state = StateOpen
			cb.lastFailure = time.Now()
			cb.failures = cb.failureThreshold // Mark as threshold reached
			// Print banner to make it super visible
			slog.Error("")
			slog.Error("═══════════════════════════════════════════════════════════════")
			slog.Error("CIRCUIT BREAKER OPENED - CRITICAL ERROR DETECTED")
			slog.Error("═══════════════════════════════════════════════════════════════")
			slog.Error("Reason: Critical API error (401/403/404) detected")
			slog.Error("Action: Fix your API key or model name and restart the server")
			slog.Error("Recovery: Circuit will attempt recovery after 30s timeout")
			slog.Error("═══════════════════════════════════════════════════════════════")
			slog.Error("")
		} else {
			// Normal failure - increment counter
			cb.failures++
			if cb.failures >= cb.failureThreshold {
				cb.state = StateOpen
				cb.lastFailure = time.Now()
				// Print banner for visibility
				slog.Error("")
				slog.Error("═══════════════════════════════════════════════════════════════")
				slog.Error("CIRCUIT BREAKER OPENED - MULTIPLE FAILURES DETECTED")
				slog.Error("═══════════════════════════════════════════════════════════════")
				slog.Error("Failures", "count", cb.failures, "threshold", cb.failureThreshold)
				slog.Error("Reason: Multiple API failures (likely transient errors)")
				slog.Error("Possible causes: Network issues, rate limits, server errors")
				slog.Error("Recovery: Circuit will attempt recovery after 30s timeout")
				slog.Error("Check logs above for detailed error messages")
				slog.Error("═══════════════════════════════════════════════════════════════")
				slog.Error("")
			}
		}

	case StateHalfOpen:
		// Failed in half-open, go back to open
		cb.state = StateOpen
		cb.lastFailure = time.Now()
		cb.failures = cb.failureThreshold
		cb.successes = 0
	}
}

func (cb *CircuitBreaker) onSuccess() {
	switch cb.state {
	case StateClosed:
		// Reset failure count on success
		cb.failures = 0

	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.successThreshold {
			// Service recovered, close circuit
			cb.state = StateClosed
			cb.failures = 0
			cb.successes = 0
		}
	}
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}
