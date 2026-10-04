package payment

import (
	"context"
	"strings"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These tests exercise PaymentServiceServer directly. The
// simulated gateway delay is left at zero so the tests measure
// payment behaviour rather than sleep; the delay has its own
// tests at the end.

func newTestServer() *Server {
	return NewServer(0)
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

// ---------- the success path ----------

func TestProcessPaymentApprovesAValidCharge(t *testing.T) {
	server := newTestServer()

	result, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
		OrderId:    "ORD0001",
		CustomerId: "C001",
		Amount:     4500,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.GetStatus() != "SUCCESS" {
		t.Errorf("status: got %q, want %q", result.GetStatus(), "SUCCESS")
	}
	if result.GetPaymentId() != "PAY0001" {
		t.Errorf("payment ID: got %q, want %q", result.GetPaymentId(), "PAY0001")
	}
	// The message is shown to the customer, so it has to name
	// the order it settled.
	if !strings.Contains(result.GetMessage(), "ORD0001") {
		t.Errorf("message %q does not mention the order", result.GetMessage())
	}
}

func TestPaymentIDsAreSequentialAndUnique(t *testing.T) {
	server := newTestServer()

	seen := make(map[string]bool)

	for i, want := range []string{"PAY0001", "PAY0002", "PAY0003"} {
		result, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
			OrderId:    "ORD0001",
			CustomerId: "C001",
			Amount:     1000,
		})
		if err != nil {
			t.Fatalf("charge %d failed: %v", i+1, err)
		}

		if result.GetPaymentId() != want {
			t.Errorf("charge %d: got %q, want %q", i+1, result.GetPaymentId(), want)
		}
		if seen[result.GetPaymentId()] {
			t.Errorf("payment ID %q was issued twice", result.GetPaymentId())
		}
		seen[result.GetPaymentId()] = true
	}
}

func TestProcessPaymentAcceptsAmountsUpToTheLimit(t *testing.T) {
	cases := []struct {
		name   string
		amount float64
	}{
		{"a small charge", 0.01},
		{"a typical charge", 12000},
		{"exactly at the limit", InsufficientFundsThreshold},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			result, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
				OrderId:    "ORD0001",
				CustomerId: "C001",
				Amount:     testCase.amount,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.GetStatus() != "SUCCESS" {
				t.Errorf("status: got %q, want SUCCESS", result.GetStatus())
			}
		})
	}
}

// ---------- failure paths ----------

func TestProcessPaymentDeclinesAmountsOverTheLimit(t *testing.T) {
	cases := []struct {
		name   string
		amount float64
	}{
		{"one over the limit", InsufficientFundsThreshold + 1},
		{"far over the limit", 500000},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			result, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
				OrderId:    "ORD0001",
				CustomerId: "C001",
				Amount:     testCase.amount,
			})

			// A decline is a business outcome, not a broken
			// service, so it must not look retryable.
			assertCode(t, err, codes.FailedPrecondition)
			if result != nil {
				t.Errorf("expected no result alongside the decline, got %v", result)
			}
		})
	}
}

func TestProcessPaymentRejectsInvalidRequests(t *testing.T) {
	cases := []struct {
		name    string
		request *pb.PaymentRequest
	}{
		{"no order ID", &pb.PaymentRequest{CustomerId: "C001", Amount: 100}},
		{"no customer ID", &pb.PaymentRequest{OrderId: "ORD0001", Amount: 100}},
		{"neither ID", &pb.PaymentRequest{Amount: 100}},
		{"zero amount", &pb.PaymentRequest{OrderId: "ORD0001", CustomerId: "C001", Amount: 0}},
		{"negative amount", &pb.PaymentRequest{OrderId: "ORD0001", CustomerId: "C001", Amount: -50}},
		{"nil request", nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := newTestServer()

			_, err := server.ProcessPayment(context.Background(), testCase.request)

			assertCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestDeclinedPaymentDoesNotConsumeAPaymentID(t *testing.T) {
	server := newTestServer()

	// A decline happens before an ID is issued, so the next
	// successful charge still gets PAY0001 — references stay
	// contiguous rather than skipping over failures.
	_, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
		OrderId: "ORD0001", CustomerId: "C001", Amount: 500000,
	})
	if err == nil {
		t.Fatal("expected the over-limit charge to be declined")
	}

	result, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
		OrderId: "ORD0002", CustomerId: "C001", Amount: 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.GetPaymentId() != "PAY0001" {
		t.Errorf("payment ID: got %q, want %q", result.GetPaymentId(), "PAY0001")
	}
}

// ---------- the simulated gateway delay ----------

func TestSimulatedDelayIsAppliedBeforeApproval(t *testing.T) {
	server := NewServer(120 * time.Millisecond)

	start := time.Now()
	_, err := server.ProcessPayment(context.Background(), &pb.PaymentRequest{
		OrderId: "ORD0001", CustomerId: "C001", Amount: 100,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 120*time.Millisecond {
		t.Errorf("call returned in %s, expected it to wait at least 120ms", elapsed)
	}
}

func TestSlowPaymentAbandonsTheChargeWhenTheCallerGivesUp(t *testing.T) {
	// This is what the Timeout resilience pattern relies on: a
	// caller whose deadline passes must get DeadlineExceeded
	// promptly rather than waiting out the full gateway delay.
	server := NewServer(3 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := server.ProcessPayment(ctx, &pb.PaymentRequest{
		OrderId: "ORD0001", CustomerId: "C001", Amount: 100,
	})
	elapsed := time.Since(start)

	assertCode(t, err, codes.DeadlineExceeded)

	if elapsed > time.Second {
		t.Errorf("took %s to give up, expected it to return as soon as the deadline passed", elapsed)
	}
}

func TestGatewayDelayReadsTheEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"unset falls back to the default", "", DefaultGatewayDelay},
		{"zero disables the delay", "0s", 0},
		{"a custom duration", "250ms", 250 * time.Millisecond},
		{"an unparseable value falls back", "not-a-duration", DefaultGatewayDelay},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("PAYMENT_DELAY", testCase.value)

			if got := GatewayDelay(); got != testCase.want {
				t.Errorf("GatewayDelay(): got %s, want %s", got, testCase.want)
			}
		})
	}
}
