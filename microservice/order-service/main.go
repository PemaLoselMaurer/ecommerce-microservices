package main

import (
	"log"
	"net"
	"os"
	"strings"
	"time"

	pb "ecommerce-microservices/proto"

	"ecommerce-microservices/internal/order"
	"ecommerce-microservices/internal/pricing"
	"ecommerce-microservices/resilience"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

// envOr returns the value of the named environment variable,
// or fallback when it is unset.
func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// dialService connects to another microservice over
// plaintext gRPC. Extra dial options (e.g. resilience
// interceptors) can be passed per connection.
func dialService(target, name string, opts ...grpc.DialOption) *grpc.ClientConn {
	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, opts...)

	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		log.Fatalf("failed to connect to %s at %s: %v", name, target, err)
	}
	return conn
}

// pricingClient connects to the calculate-order-price Worker.
// Its address, credential and timeout all come from the
// environment; nothing about the deployment is compiled in.
//
//	PRICING_URL             the Worker's base URL
//	PRICING_API_TOKEN_FILE  file holding the bearer token, or
//	PRICING_API_TOKEN       the token itself
//	PRICING_TIMEOUT         per-call budget, e.g. 3s
func pricingClient() pricing.Client {
	url := os.Getenv("PRICING_URL")
	if url == "" {
		log.Printf("warning: PRICING_URL is unset; orders will be refused until pricing is configured")
		return pricing.Unconfigured{}
	}

	token := os.Getenv("PRICING_API_TOKEN")
	if path := os.Getenv("PRICING_API_TOKEN_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("failed to read PRICING_API_TOKEN_FILE: %v", err)
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" {
		log.Printf("warning: no pricing API token configured; the pricing service will refuse every call")
	}

	timeout := pricing.DefaultTimeout
	if raw := os.Getenv("PRICING_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			log.Fatalf("invalid PRICING_TIMEOUT %q: %v", raw, err)
		}
		timeout = parsed
	}

	log.Printf("pricing via %s (timeout %s)", url, timeout)
	return pricing.NewHTTPClient(url, token, timeout)
}

func main() {

	customerConn := dialService(envOr("CUSTOMER_ADDR", "localhost:50052"), "Customer Service")
	defer customerConn.Close()

	// If Product Service is stopped/unreachable, calls fail fast
	// on their own (connection refused), so 3 failures trip the
	// breaker almost immediately and further calls are rejected
	// client-side without even attempting to connect.
	productBreaker := resilience.NewCircuitBreaker("product-service", resilience.DefaultCircuitBreakerConfig())
	productConn := dialService(envOr("PRODUCT_ADDR", "localhost:50051"), "Product Service",
		grpc.WithUnaryInterceptor(productBreaker.UnaryClientInterceptor()),
	)
	defer productConn.Close()

	// Inventory's ReserveStock is simulated as flaky, so this
	// connection retries transient failures with backoff instead
	// of failing the whole order on the first hiccup.
	inventoryConn := dialService(envOr("INVENTORY_ADDR", "localhost:50053"), "Inventory Service",
		grpc.WithUnaryInterceptor(resilience.UnaryClientInterceptor(resilience.DefaultRetryConfig())),
	)
	defer inventoryConn.Close()

	// Payment Service is simulated as slow, so every call here
	// times out (below) and counts as a failure. Once 3 calls in
	// a row fail, the circuit breaker trips open and further
	// calls fail immediately instead of each paying the full 1s
	// timeout on a dependency that's known to be down; it tries
	// again after 5s.
	paymentBreaker := resilience.NewCircuitBreaker("payment-service", resilience.DefaultCircuitBreakerConfig())
	paymentConn := dialService(envOr("PAYMENT_ADDR", "localhost:50054"), "Payment Service",
		grpc.WithChainUnaryInterceptor(
			paymentBreaker.UnaryClientInterceptor(),
			resilience.TimeoutUnaryClientInterceptor(1*time.Second),
		),
	)
	defer paymentConn.Close()

	notificationConn := dialService(envOr("NOTIFICATION_ADDR", "localhost:50055"), "Notification Service")
	defer notificationConn.Close()

	orderServer := order.NewServer(order.Clients{
		Customer:     pb.NewCustomerServiceClient(customerConn),
		Product:      pb.NewProductServiceClient(productConn),
		Inventory:    pb.NewInventoryServiceClient(inventoryConn),
		Payment:      pb.NewPaymentServiceClient(paymentConn),
		Notification: pb.NewNotificationServiceClient(notificationConn),
		Pricing:      pricingClient(),
	})

	addr := envOr("ORDER_LISTEN", ":50056")

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer()

	pb.RegisterOrderServiceServer(grpcServer, orderServer)
	reflection.Register(grpcServer)

	log.Printf("Order Service is running on %s...", addr)

	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("failed to start gRPC server: %v", err)
	}
}
