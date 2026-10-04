package resilience

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type circuitState int

const (
	closed circuitState = iota
	open
	halfOpen
)

func (s circuitState) String() string {
	switch s {
	case closed:
		return "CLOSED"
	case open:
		return "OPEN"
	case halfOpen:
		return "HALF-OPEN"
	default:
		return "UNKNOWN"
	}
}

// CircuitBreakerConfig controls a CircuitBreaker.
type CircuitBreakerConfig struct {
	// FailureThreshold is how many consecutive failures while
	// Closed trip the breaker to Open.
	FailureThreshold int
	// ResetTimeout is how long the breaker stays Open before
	// letting a single trial call through (Half-Open).
	ResetTimeout time.Duration
}

// DefaultCircuitBreakerConfig trips after 3 consecutive
// failures and stays open for 5 seconds before trying again.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     5 * time.Second,
	}
}

// CircuitBreaker implements the Circuit Breaker resilience
// pattern for a single downstream connection. While Closed,
// calls go through normally. Once FailureThreshold consecutive
// calls fail, it trips Open and rejects every call immediately
// for ResetTimeout — without touching the network or waiting
// on any per-call timeout — so a dependency that's known to be
// down doesn't keep costing every caller the full timeout on
// every request. After ResetTimeout it goes Half-Open and lets
// one trial call through: success closes the breaker, failure
// reopens it.
type CircuitBreaker struct {
	name string
	cfg  CircuitBreakerConfig

	mu       sync.Mutex
	state    circuitState
	failures int
	openedAt time.Time
}

// NewCircuitBreaker creates a breaker for one downstream
// dependency, identified by name for logging.
func NewCircuitBreaker(name string, cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{name: name, cfg: cfg, state: closed}
}

// UnaryClientInterceptor returns a gRPC interceptor that
// applies this breaker to whatever connection it's attached
// to.
func (cb *CircuitBreaker) UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {

		if !cb.allow(method) {
			return status.Error(codes.Unavailable, fmt.Sprintf(
				"circuit breaker %q is open, failing fast without calling %s", cb.name, method,
			))
		}

		err := invoker(ctx, method, req, reply, cc, opts...)
		cb.record(err == nil)
		return err
	}
}

// allow reports whether a call may proceed, transitioning
// Open -> Half-Open once ResetTimeout has elapsed. It logs the
// current state on every call so the state is visible in the
// terminal even between transitions, not just at the moment
// the state changes.
func (cb *CircuitBreaker) allow(method string) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state != open {
		log.Printf("[circuit-breaker] %q: state=%s, calling %s", cb.name, cb.state, method)
		return true
	}

	if time.Since(cb.openedAt) < cb.cfg.ResetTimeout {
		log.Printf("[circuit-breaker] %q: state=%s, rejecting %s without calling it", cb.name, cb.state, method)
		return false
	}

	cb.state = halfOpen
	log.Printf("[circuit-breaker] %q: OPEN -> HALF-OPEN, letting one trial call through", cb.name)
	return true
}

// record updates breaker state based on the outcome of a call
// that was allowed through.
func (cb *CircuitBreaker) record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if success {
		if cb.state != closed {
			log.Printf("[circuit-breaker] %q: %s -> CLOSED, call succeeded", cb.name, cb.state)
		}
		cb.state = closed
		cb.failures = 0
		return
	}

	switch cb.state {
	case halfOpen:
		log.Printf("[circuit-breaker] %q: HALF-OPEN -> OPEN, trial call failed", cb.name)
		cb.state = open
		cb.openedAt = time.Now()
	case closed:
		cb.failures++
		if cb.failures >= cb.cfg.FailureThreshold {
			log.Printf("[circuit-breaker] %q: CLOSED -> OPEN, %d consecutive failures, rejecting calls for %s",
				cb.name, cb.failures, cb.cfg.ResetTimeout)
			cb.state = open
			cb.openedAt = time.Now()
		}
	}
}
