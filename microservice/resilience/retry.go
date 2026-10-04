// Package resilience holds gRPC client-side resilience
// patterns (retry, timeout, circuit breaker) used by the lab.
package resilience

import (
	"context"
	"log"
	"math"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RetryConfig controls the retry pattern applied by
// UnaryClientInterceptor.
type RetryConfig struct {
	// MaxAttempts is the total number of times the RPC is
	// attempted, including the first (non-retry) call.
	MaxAttempts int
	// BaseDelay is the wait before the first retry. Each
	// subsequent retry doubles it, up to MaxDelay.
	BaseDelay time.Duration
	MaxDelay  time.Duration
	// RetryableCodes lists the status codes worth retrying.
	// Anything else (e.g. InvalidArgument, NotFound) is
	// returned to the caller immediately, since retrying a
	// request that is wrong or was correctly rejected can't
	// turn it into a success.
	RetryableCodes map[codes.Code]bool
}

// DefaultRetryConfig retries transient-looking failures
// (Unavailable, DeadlineExceeded) up to 3 times with
// exponential backoff.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   200 * time.Millisecond,
		MaxDelay:    2 * time.Second,
		RetryableCodes: map[codes.Code]bool{
			codes.Unavailable:      true,
			codes.DeadlineExceeded: true,
		},
	}
}

// UnaryClientInterceptor implements the Retry resilience
// pattern for unary gRPC calls: on a retryable error it waits
// with exponential backoff and calls the RPC again, up to
// cfg.MaxAttempts attempts total. It stops early if the error
// isn't retryable or the call's context is done.
func UnaryClientInterceptor(cfg RetryConfig) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {

		var lastErr error

		for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
			lastErr = invoker(ctx, method, req, reply, cc, opts...)
			if lastErr == nil {
				if attempt > 1 {
					log.Printf("[retry] %s succeeded on attempt %d/%d", method, attempt, cfg.MaxAttempts)
				}
				return nil
			}

			st, _ := status.FromError(lastErr)
			retryable := cfg.RetryableCodes[st.Code()]

			if !retryable || attempt == cfg.MaxAttempts {
				if attempt > 1 {
					log.Printf("[retry] %s giving up after %d/%d attempts: %v", method, attempt, cfg.MaxAttempts, lastErr)
				}
				return lastErr
			}

			delay := backoff(cfg, attempt)
			log.Printf("[retry] %s attempt %d/%d failed (%s), retrying in %s", method, attempt, cfg.MaxAttempts, st.Code(), delay)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		return lastErr
	}
}

// backoff returns the exponential delay before the given
// retry attempt, capped at cfg.MaxDelay.
func backoff(cfg RetryConfig, attempt int) time.Duration {
	delay := time.Duration(float64(cfg.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay > cfg.MaxDelay {
		return cfg.MaxDelay
	}
	return delay
}
