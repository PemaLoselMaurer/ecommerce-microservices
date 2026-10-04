// Package payment holds the Payment Service implementation.
package payment

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	pb "ecommerce-microservices/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InsufficientFundsThreshold is the amount above which a
// payment is declined, making success and failure
// deterministically demoable.
const InsufficientFundsThreshold = 100000

// DefaultGatewayDelay simulates a slow upstream payment
// gateway, so the Timeout resilience pattern has something to
// bound. It fires on every call: the Order Service's per-call
// timeout for this connection (1s) is shorter than this delay,
// so ProcessPayment always gets cut off client-side instead of
// running to completion.
//
// Set PAYMENT_DELAY (any Go duration, e.g. "0s" or "800ms") to
// override it — running with PAYMENT_DELAY=0s makes orders
// succeed, which is what the happy-path tests need.
const DefaultGatewayDelay = 3 * time.Second

// Server implements the PaymentService defined in
// payment.proto.
type Server struct {
	pb.UnimplementedPaymentServiceServer

	// delay is the simulated gateway latency applied to every
	// call. Kept as a field rather than a constant so tests can
	// set it to zero.
	delay time.Duration

	mu            sync.Mutex
	nextPaymentID int
}

// NewServer builds a Payment Service that waits delay before
// settling each charge.
func NewServer(delay time.Duration) *Server {
	return &Server{delay: delay}
}

// GatewayDelay reads the simulated delay from PAYMENT_DELAY,
// falling back to DefaultGatewayDelay when it is unset or
// unparseable.
func GatewayDelay() time.Duration {
	raw := os.Getenv("PAYMENT_DELAY")
	if raw == "" {
		return DefaultGatewayDelay
	}

	delay, err := time.ParseDuration(raw)
	if err != nil {
		log.Printf("ignoring invalid PAYMENT_DELAY %q: %v", raw, err)
		return DefaultGatewayDelay
	}
	return delay
}

// ProcessPayment charges the given amount for an order,
// declining amounts over the insufficient-funds threshold.
func (s *Server) ProcessPayment(
	ctx context.Context,
	req *pb.PaymentRequest,
) (*pb.PaymentResult, error) {

	if req.GetOrderId() == "" || req.GetCustomerId() == "" {
		return nil, status.Error(
			codes.InvalidArgument,
			"order ID and customer ID are required",
		)
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"amount must be greater than zero",
		)
	}

	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-time.After(s.delay):
		}
	}

	if req.GetAmount() > InsufficientFundsThreshold {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"payment declined: insufficient funds for amount %.2f",
			req.GetAmount(),
		)
	}

	s.mu.Lock()
	s.nextPaymentID++
	paymentID := fmt.Sprintf("PAY%04d", s.nextPaymentID)
	s.mu.Unlock()

	return &pb.PaymentResult{
		PaymentId: paymentID,
		Status:    "SUCCESS",
		Message:   fmt.Sprintf("payment of %.2f approved for order %s", req.GetAmount(), req.GetOrderId()),
	}, nil
}
