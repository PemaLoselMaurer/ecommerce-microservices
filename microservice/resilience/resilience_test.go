package resilience

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests drive the interceptors against a stub invoker
// rather than a real connection, so they run in microseconds
// and can reproduce failures a live dependency could not be
// made to produce on demand.

// scriptedInvoker stands in for the gRPC call an interceptor
// wraps. It returns the next error from its script on each
// call and records how many times it was invoked.
type scriptedInvoker struct {
	mu      sync.Mutex
	script  []error
	calls   int
	delay   time.Duration
	methods []string
}

func (s *scriptedInvoker) invoke(
	ctx context.Context,
	method string,
	req, reply interface{},
	cc *grpc.ClientConn,
	opts ...grpc.CallOption,
) error {
	s.mu.Lock()
	attempt := s.calls
	s.calls++
	s.methods = append(s.methods, method)
	delay := s.delay
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return status.FromContextError(ctx.Err()).Err()
		case <-time.After(delay):
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if attempt < len(s.script) {
		return s.script[attempt]
	}
	// Past the end of the script every call succeeds.
	return nil
}

func (s *scriptedInvoker) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected a gRPC status error, got %T: %v", err, err)
	}
	if st.Code() != want {
		t.Fatalf("expected code %s, got %s (%q)", want, st.Code(), st.Message())
	}
}

// fastRetry keeps the backoff short so the tests do not spend
// their time asleep.
func fastRetry() RetryConfig {
	cfg := DefaultRetryConfig()
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = 5 * time.Millisecond
	return cfg
}

// ==========================================================
// Retry
// ==========================================================

func TestRetrySucceedsFirstTimeWithoutRetrying(t *testing.T) {
	invoker := &scriptedInvoker{}
	interceptor := UnaryClientInterceptor(fastRetry())

	err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if invoker.callCount() != 1 {
		t.Errorf("made %d calls, want 1 when the first attempt succeeds", invoker.callCount())
	}
}

func TestRetryRecoversFromTransientFailures(t *testing.T) {
	// Two Unavailable errors then success — exactly what the
	// Inventory Service's simulated flakiness produces.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "temporarily unavailable"),
		status.Error(codes.Unavailable, "temporarily unavailable"),
	}}
	interceptor := UnaryClientInterceptor(fastRetry())

	err := interceptor(context.Background(), "/inventory/ReserveStock", nil, nil, nil, invoker.invoke)
	if err != nil {
		t.Fatalf("the call should have recovered on the third attempt, got: %v", err)
	}

	if invoker.callCount() != 3 {
		t.Errorf("made %d calls, want 3", invoker.callCount())
	}
}

func TestRetryStopsAtMaxAttempts(t *testing.T) {
	// A dependency that never recovers must not be retried
	// forever.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
	}}

	cfg := fastRetry()
	interceptor := UnaryClientInterceptor(cfg)

	err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)

	assertCode(t, err, codes.Unavailable)

	if invoker.callCount() != cfg.MaxAttempts {
		t.Errorf("made %d calls, want %d", invoker.callCount(), cfg.MaxAttempts)
	}
}

func TestRetryDoesNotRetryPermanentErrors(t *testing.T) {
	// Retrying a request that was correctly rejected cannot
	// turn it into a success, and would multiply the load on a
	// service that is working fine.
	cases := []struct {
		name string
		code codes.Code
	}{
		{"not found", codes.NotFound},
		{"invalid argument", codes.InvalidArgument},
		{"insufficient stock", codes.ResourceExhausted},
		{"payment declined", codes.FailedPrecondition},
		{"unauthenticated", codes.Unauthenticated},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			invoker := &scriptedInvoker{script: []error{
				status.Error(testCase.code, "permanent"),
				status.Error(testCase.code, "permanent"),
			}}
			interceptor := UnaryClientInterceptor(fastRetry())

			err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)

			assertCode(t, err, testCase.code)

			if invoker.callCount() != 1 {
				t.Errorf("made %d calls, want 1 — %s must not be retried",
					invoker.callCount(), testCase.code)
			}
		})
	}
}

func TestRetryRetriesDeadlineExceeded(t *testing.T) {
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.DeadlineExceeded, "slow"),
	}}
	interceptor := UnaryClientInterceptor(fastRetry())

	if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if invoker.callCount() != 2 {
		t.Errorf("made %d calls, want 2", invoker.callCount())
	}
}

func TestRetryStopsWhenTheCallerGivesUp(t *testing.T) {
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
	}}

	cfg := DefaultRetryConfig()
	cfg.BaseDelay = 200 * time.Millisecond // longer than the deadline below
	interceptor := UnaryClientInterceptor(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := interceptor(ctx, "/test/Method", nil, nil, nil, invoker.invoke)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error once the caller's context expired")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the context's deadline error, got %v", err)
	}
	// It must abandon the backoff rather than sleep it out.
	if elapsed > 150*time.Millisecond {
		t.Errorf("waited %s before giving up, expected it to stop as soon as the context expired", elapsed)
	}
}

func TestBackoffGrowsExponentiallyAndIsCapped(t *testing.T) {
	cfg := RetryConfig{BaseDelay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 300 * time.Millisecond}, // 400ms, capped at MaxDelay
		{4, 300 * time.Millisecond}, // 800ms, capped
	}

	for _, testCase := range cases {
		if got := backoff(cfg, testCase.attempt); got != testCase.want {
			t.Errorf("backoff(attempt %d): got %s, want %s", testCase.attempt, got, testCase.want)
		}
	}
}

// ==========================================================
// Timeout
// ==========================================================

func TestTimeoutAllowsACallThatFinishesInBudget(t *testing.T) {
	invoker := &scriptedInvoker{delay: 10 * time.Millisecond}
	interceptor := TimeoutUnaryClientInterceptor(500 * time.Millisecond)

	if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTimeoutCutsOffASlowDependency(t *testing.T) {
	// The Payment Service's 3-second gateway delay against a
	// 1-second budget, in miniature.
	invoker := &scriptedInvoker{delay: 2 * time.Second}
	interceptor := TimeoutUnaryClientInterceptor(80 * time.Millisecond)

	start := time.Now()
	err := interceptor(context.Background(), "/payment/ProcessPayment", nil, nil, nil, invoker.invoke)
	elapsed := time.Since(start)

	assertCode(t, err, codes.DeadlineExceeded)

	if elapsed > time.Second {
		t.Errorf("took %s to give up on an 80ms budget", elapsed)
	}
}

func TestTimeoutDoesNotExtendAShorterCallerDeadline(t *testing.T) {
	invoker := &scriptedInvoker{delay: 2 * time.Second}

	// The interceptor's budget is generous, but the caller's
	// own deadline is tight; the tighter one has to win.
	interceptor := TimeoutUnaryClientInterceptor(5 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := interceptor(ctx, "/test/Method", nil, nil, nil, invoker.invoke)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the call to be cut off by the caller's deadline")
	}
	if elapsed > time.Second {
		t.Errorf("took %s, expected the caller's 60ms deadline to apply", elapsed)
	}
}

// ==========================================================
// Circuit breaker
// ==========================================================

func fastBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     80 * time.Millisecond,
	}
}

func TestBreakerStaysClosedWhileCallsSucceed(t *testing.T) {
	breaker := NewCircuitBreaker("test", fastBreakerConfig())
	invoker := &scriptedInvoker{}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < 10; i++ {
		if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err != nil {
			t.Fatalf("call %d failed unexpectedly: %v", i+1, err)
		}
	}

	if invoker.callCount() != 10 {
		t.Errorf("made %d calls, want 10 — a closed breaker must not block anything",
			invoker.callCount())
	}
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	// Every call to the dependency fails.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
	}}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < cfg.FailureThreshold; i++ {
		if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err == nil {
			t.Fatalf("call %d should have failed", i+1)
		}
	}

	if invoker.callCount() != cfg.FailureThreshold {
		t.Fatalf("made %d calls, want %d before the breaker trips",
			invoker.callCount(), cfg.FailureThreshold)
	}

	// The next call must be rejected without touching the
	// dependency at all — that is the whole point of the
	// pattern.
	err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)

	assertCode(t, err, codes.Unavailable)

	if invoker.callCount() != cfg.FailureThreshold {
		t.Errorf("the open breaker still called the dependency: %d calls, want %d",
			invoker.callCount(), cfg.FailureThreshold)
	}
}

func TestOpenBreakerFailsFast(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	// A dependency that hangs. Once the breaker is open the
	// caller should not pay that cost again.
	invoker := &scriptedInvoker{
		delay: 300 * time.Millisecond,
		script: []error{
			status.Error(codes.Unavailable, "down"),
			status.Error(codes.Unavailable, "down"),
			status.Error(codes.Unavailable, "down"),
		},
	}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < cfg.FailureThreshold; i++ {
		_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	}

	start := time.Now()
	_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Errorf("the open breaker took %s to reject a call, expected it to be immediate", elapsed)
	}
}

func TestBreakerDoesNotTripOnScatteredFailures(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	// Fail, succeed, fail, succeed, fail: three failures in
	// total, but never three in a row, so the breaker must
	// stay closed.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		nil,
		status.Error(codes.Unavailable, "down"),
		nil,
		status.Error(codes.Unavailable, "down"),
	}}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < 5; i++ {
		_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	}

	// A sixth call must still reach the dependency.
	before := invoker.callCount()
	_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)

	if invoker.callCount() != before+1 {
		t.Error("the breaker tripped on non-consecutive failures")
	}
}

func TestBreakerClosesAgainWhenTheDependencyRecovers(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	// Three failures, then the dependency comes back.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
	}}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < cfg.FailureThreshold; i++ {
		_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	}

	// While open, calls are rejected.
	if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err == nil {
		t.Fatal("expected the open breaker to reject this call")
	}

	// After the reset timeout it goes half-open and lets one
	// trial call through, which now succeeds and closes it.
	time.Sleep(cfg.ResetTimeout + 20*time.Millisecond)

	if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err != nil {
		t.Fatalf("the trial call should have succeeded: %v", err)
	}

	// Fully closed again: further calls go straight through.
	for i := 0; i < 3; i++ {
		if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err != nil {
			t.Fatalf("call %d after recovery failed: %v", i+1, err)
		}
	}
}

func TestBreakerReopensWhenTheTrialCallFails(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	// The dependency never recovers, so the half-open trial
	// fails and the breaker must open again rather than let
	// traffic back through.
	invoker := &scriptedInvoker{script: []error{
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"),
		status.Error(codes.Unavailable, "down"), // the trial call
	}}
	interceptor := breaker.UnaryClientInterceptor()

	for i := 0; i < cfg.FailureThreshold; i++ {
		_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	}

	time.Sleep(cfg.ResetTimeout + 20*time.Millisecond)

	// The trial call reaches the dependency and fails.
	if err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke); err == nil {
		t.Fatal("expected the trial call to fail")
	}
	callsAfterTrial := invoker.callCount()

	// And the breaker is open again immediately.
	err := interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
	assertCode(t, err, codes.Unavailable)

	if invoker.callCount() != callsAfterTrial {
		t.Error("the breaker did not reopen after the trial call failed")
	}
}

func TestBreakerIsSafeUnderConcurrentUse(t *testing.T) {
	breaker := NewCircuitBreaker("test", fastBreakerConfig())
	invoker := &scriptedInvoker{}
	interceptor := breaker.UnaryClientInterceptor()

	var wait sync.WaitGroup
	for i := 0; i < 50; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = interceptor(context.Background(), "/test/Method", nil, nil, nil, invoker.invoke)
		}()
	}
	wait.Wait()

	if invoker.callCount() != 50 {
		t.Errorf("made %d calls, want 50", invoker.callCount())
	}
}

// ==========================================================
// The patterns working together
// ==========================================================

func TestRetryAndBreakerChained(t *testing.T) {
	cfg := fastBreakerConfig()
	breaker := NewCircuitBreaker("test", cfg)

	retry := UnaryClientInterceptor(fastRetry())
	breakerInterceptor := breaker.UnaryClientInterceptor()

	// A dependency that is down. Retry exhausts its attempts on
	// each call; the breaker sees each exhausted call as one
	// failure and trips after three of them.
	invoker := &scriptedInvoker{script: make([]error, 40)}
	for i := range invoker.script {
		invoker.script[i] = status.Error(codes.Unavailable, "down")
	}

	// Breaker outermost, retry innermost — the order used for
	// the Inventory connection in order-service.
	chained := func(ctx context.Context, method string) error {
		return breakerInterceptor(ctx, method, nil, nil, nil,
			func(ctx context.Context, method string, req, reply interface{},
				cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				return retry(ctx, method, req, reply, cc, invoker.invoke)
			})
	}

	for i := 0; i < cfg.FailureThreshold; i++ {
		if err := chained(context.Background(), "/test/Method"); err == nil {
			t.Fatalf("call %d should have failed", i+1)
		}
	}

	// 3 breaker-level failures × 3 retry attempts each.
	if invoker.callCount() != 9 {
		t.Errorf("the dependency saw %d calls, want 9", invoker.callCount())
	}

	// Now the breaker is open: no further attempts at all, so
	// the retry loop never even starts.
	before := invoker.callCount()
	err := chained(context.Background(), "/test/Method")

	assertCode(t, err, codes.Unavailable)

	if invoker.callCount() != before {
		t.Errorf("the open breaker still allowed %d calls through",
			invoker.callCount()-before)
	}
}
