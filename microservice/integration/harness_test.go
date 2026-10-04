// Package integration holds the Part C integration tests.
//
// Unlike the unit tests, nothing here is a mock of the code
// under test: every service is its real implementation, and
// every call between services is a real gRPC call that is
// serialised, sent over a connection, and answered by a real
// gRPC server — interceptors, status codes and all.
//
// The connection is a bufconn (an in-memory net.Listener)
// rather than a TCP port. That keeps the tests hermetic and
// repeatable: they need no free ports, cannot collide with a
// running demo, and leave nothing behind. What they lose
// relative to TCP — kernel buffering and real latency — is not
// what these tests are measuring.
package integration

import (
	"context"
	"net"
	"testing"
	"time"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/customer"
	"ecommerce-microservices/internal/inventory"
	"ecommerce-microservices/internal/notification"
	"ecommerce-microservices/internal/payment"
	"ecommerce-microservices/internal/product"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// bufSize is the in-memory listener's buffer. Large enough
// that nothing under test ever blocks on it.
const bufSize = 1024 * 1024

func TestMain(m *testing.M) {
	// Hashing at production cost would add tens of seconds to
	// this suite without testing anything the unit tests do not
	// already cover.
	customer.HashCost = bcrypt.MinCost
	m.Run()
}

// service is a real gRPC server running on an in-memory
// listener, plus the controls a test needs to break it.
type service struct {
	name     string
	listener *bufconn.Listener
	server   *grpc.Server
	stopped  bool
}

// startService registers an implementation on a real gRPC
// server and starts serving it over a bufconn listener.
// register is called with the server so the caller can bind
// whichever service descriptor it needs.
func startService(t *testing.T, name string, register func(*grpc.Server)) *service {
	t.Helper()

	listener := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	register(grpcServer)

	svc := &service{name: name, listener: listener, server: grpcServer}

	go func() {
		// Serve returns when the server is stopped, which is a
		// normal end rather than a failure.
		_ = grpcServer.Serve(listener)
	}()

	t.Cleanup(svc.stop)

	return svc
}

// stop shuts the service down, which is how these tests
// reproduce "the dependency is unavailable": callers holding a
// connection to it start getting Unavailable, exactly as they
// would against a process that had been killed.
func (s *service) stop() {
	if s.stopped {
		return
	}
	s.stopped = true
	s.server.Stop()
	_ = s.listener.Close()
}

// dial opens a real gRPC client connection to the service.
// Extra options let a test attach the resilience interceptors
// the production wiring uses.
func (s *service) dial(t *testing.T, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()

	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return s.listener.DialContext(ctx)
		}),
	}, opts...)

	conn, err := grpc.NewClient("passthrough://bufnet", dialOpts...)
	if err != nil {
		t.Fatalf("failed to connect to %s: %v", s.name, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// ---------- starting each real service ----------

func startProduct(t *testing.T, products map[string]*pb.Product) *service {
	t.Helper()
	return startService(t, "Product Service", func(s *grpc.Server) {
		pb.RegisterProductServiceServer(s, product.NewServer(products))
	})
}

func startCustomer(t *testing.T) *service {
	t.Helper()
	return startService(t, "Customer Service", func(s *grpc.Server) {
		pb.RegisterCustomerServiceServer(s, customer.NewServer(customer.SeedAccounts()))
	})
}

func startInventory(t *testing.T, stock map[string]int32, flaky bool) *service {
	t.Helper()
	return startService(t, "Inventory Service", func(s *grpc.Server) {
		pb.RegisterInventoryServiceServer(s, inventory.NewServer(stock, flaky))
	})
}

func startPayment(t *testing.T, delay time.Duration) *service {
	t.Helper()
	return startService(t, "Payment Service", func(s *grpc.Server) {
		pb.RegisterPaymentServiceServer(s, payment.NewServer(delay))
	})
}

func startNotification(t *testing.T) *service {
	t.Helper()
	return startService(t, "Notification Service", func(s *grpc.Server) {
		pb.RegisterNotificationServiceServer(s, notification.NewServer())
	})
}

// ---------- assertions ----------

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

// callContext gives every call a deadline, so a test can never
// hang the suite if something goes wrong.
func callContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
